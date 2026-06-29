package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	symbolKindFunction    = 12
	symbolKindVariable    = 13
	symbolKindConstant    = 14
	symbolKindMethod      = 6
	symbolKindConstructor = 9
)

type message map[string]any

type responseResult struct {
	message message
	err     error
}

type position struct {
	line      int
	character int
}

type rangeValue struct {
	start position
	end   position
}

type declaration struct {
	name      string
	kind      int
	rng       rangeValue
	selection rangeValue
	calls     []string
	jsxRoots  []*treeNode
	component bool
	bodyStart int
	bodyEnd   int
}

type notificationWaiter struct {
	method    string
	predicate func(message) bool
	ch        chan message
}

type lspClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	rootURI  string
	rootName string
	trace    bool

	nextID int

	writeMu sync.Mutex
	printMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan responseResult
	waiters []*notificationWaiter
}

type sourceIndex struct {
	text      string
	lineStart []int
}

type treeNode struct {
	name     string
	props    []propValue
	children []*treeNode
}

type propValue struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type fileAnalysis struct {
	path         string
	declarations []*declaration
	roots        []*declaration
	imports      map[string]string
}

type analyzer struct {
	client *lspClient
	cache  map[string]*fileAnalysis
}

type jsonTreeOutput struct {
	File  string         `json:"file"`
	Roots []jsonTreeNode `json:"roots"`
}

type jsonTreeNode struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind,omitempty"`
	File     string         `json:"file,omitempty"`
	Props    []propValue    `json:"props,omitempty"`
	Cycle    bool           `json:"cycle,omitempty"`
	Children []jsonTreeNode `json:"children,omitempty"`
}

func Run(args []string) {
	fs := flag.NewFlagSet("lsp", flag.ExitOnError)
	trace := fs.Bool("trace-lsp", false, "print raw LSP messages")
	maxDepth := fs.Int("max-depth", 1, "expand relative imported components up to this depth")
	format := fs.String("format", "text", "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: agent-role lsp [--format text|json] [--max-depth N] [--trace-lsp] <file.ts|file.tsx>\n")
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}

	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	if *maxDepth < 1 {
		*maxDepth = 1
	}

	outputFormat := strings.ToLower(strings.TrimSpace(*format))
	if *jsonOutput {
		outputFormat = "json"
	}
	if outputFormat != "text" && outputFormat != "json" {
		fmt.Fprintln(os.Stderr, "--format must be text or json")
		os.Exit(2)
	}

	if err := run(fs.Arg(0), *trace, *maxDepth, outputFormat); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inputPath string, trace bool, maxDepth int, outputFormat string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return err
	}

	documentPath, err := filepath.Abs(inputPath)
	if err != nil {
		return err
	}

	serverName, serverArgs, err := resolveServerCommand(rootDir)
	if err != nil {
		return err
	}

	client, err := startClient(serverName, serverArgs, rootDir, trace)
	if err != nil {
		return err
	}
	defer client.kill()

	go client.readLoop()

	if err := client.initialize(); err != nil {
		return err
	}
	client.notify("initialized", message{})

	analyzer := &analyzer{client: client, cache: map[string]*fileAnalysis{}}
	analysis, err := analyzer.analyzeFile(documentPath)
	if err != nil {
		return err
	}
	if outputFormat == "json" {
		if err := printJSONTree(analysis, analyzer, maxDepth); err != nil {
			return err
		}
	} else {
		printTree(analysis, analyzer, maxDepth)
	}

	if _, err := client.request("shutdown", nil); err != nil {
		return err
	}
	client.notify("exit", nil)
	_ = client.stdin.Close()
	return client.cmd.Wait()
}

func (a *analyzer) analyzeFile(path string) (*fileAnalysis, error) {
	documentPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	if cached := a.cache[documentPath]; cached != nil {
		return cached, nil
	}

	textBytes, err := os.ReadFile(documentPath)
	if err != nil {
		return nil, err
	}
	sourceText := string(textBytes)
	documentURI := fileURI(documentPath)

	a.client.notify("textDocument/didOpen", message{
		"textDocument": message{
			"uri":        documentURI,
			"languageId": languageID(documentPath),
			"version":    1,
			"text":       sourceText,
		},
	})

	resp, err := a.client.request("textDocument/documentSymbol", message{
		"textDocument": message{"uri": documentURI},
	})
	if err != nil {
		return nil, err
	}

	declarations := collectDeclarations(resp["result"])
	declarations = filterSourceDeclarations(sourceText, declarations)
	assignBodyRanges(sourceText, declarations)
	roots := buildTree(sourceText, declarations)

	analysis := &fileAnalysis{
		path:         documentPath,
		declarations: declarations,
		roots:        roots,
		imports:      parseImports(sourceText, documentPath),
	}
	a.cache[documentPath] = analysis

	return analysis, nil
}

func (c *lspClient) initialize() error {
	_, err := c.request("initialize", message{
		"processId": os.Getpid(),
		"rootUri":   c.rootURI,
		"capabilities": message{
			"textDocument": message{
				"documentSymbol": message{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"publishDiagnostics": message{
					"relatedInformation": true,
				},
			},
			"workspace": message{
				"configuration":    true,
				"workspaceFolders": true,
			},
			"window": message{
				"workDoneProgress": true,
			},
		},
		"workspaceFolders": []message{{"uri": c.rootURI, "name": c.rootName}},
	})
	return err
}

func resolveServerCommand(rootDir string) (string, []string, error) {
	localBin := filepath.Join(rootDir, "node_modules", ".bin", serverBinaryName())
	if _, err := os.Stat(localBin); err == nil {
		return localBin, []string{"--stdio"}, nil
	}

	if bin, err := exec.LookPath("typescript-language-server"); err == nil {
		return bin, []string{"--stdio"}, nil
	}

	npx, err := exec.LookPath("npx")
	if err != nil {
		return "", nil, fmt.Errorf("typescript-language-server를 찾을 수 없습니다. npm install 또는 npx가 필요합니다")
	}

	return npx, []string{
		"-y",
		"-p", "typescript-language-server@3.3.2",
		"-p", "typescript@5.0.4",
		"typescript-language-server",
		"--stdio",
	}, nil
}

func startClient(name string, args []string, rootDir string, trace bool) (*lspClient, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = rootDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if trace {
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
		go printStderr(stderr)
	} else {
		cmd.Stderr = io.Discard
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &lspClient{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		rootURI:  fileURI(rootDir + string(os.PathSeparator)),
		rootName: filepath.Base(rootDir),
		trace:    trace,
		nextID:   1,
		pending:  map[string]chan responseResult{},
	}, nil
}

func (c *lspClient) request(method string, params any) (message, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	ch := make(chan responseResult, 1)
	c.pending[strconv.Itoa(id)] = ch
	c.mu.Unlock()

	c.send(message{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})

	select {
	case result := <-ch:
		return result.message, result.err
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("timed out waiting for %s", method)
	}
}

func (c *lspClient) notify(method string, params any) {
	payload := message{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	c.send(payload)
}

func (c *lspClient) send(payload message) {
	c.printMessage("-->", payload)

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := c.stdin.Write(append([]byte(header), body...)); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func (c *lspClient) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		msg, err := readMessage(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				fmt.Fprintln(os.Stderr, err)
			}
			return
		}
		c.handleMessage(msg)
	}
}

func (c *lspClient) handleMessage(msg message) {
	c.printMessage("<--", msg)

	id, hasID := msg["id"]
	method, hasMethod := msg["method"].(string)

	if hasID && hasMethod {
		c.send(message{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  c.resultForServerRequest(method, msg),
		})
		return
	}

	if hasID {
		key := idKey(id)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()

		if ch != nil {
			if errPayload, ok := msg["error"]; ok {
				ch <- responseResult{message: msg, err: fmt.Errorf("lsp error response: %v", errPayload)}
			} else {
				ch <- responseResult{message: msg}
			}
		}
		return
	}

	if hasMethod {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, waiter := range c.waiters {
			if waiter.method == method && waiter.predicate(msg) {
				c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
				waiter.ch <- msg
				return
			}
		}
	}
}

func (c *lspClient) resultForServerRequest(method string, msg message) any {
	switch method {
	case "workspace/configuration":
		params, _ := msg["params"].(map[string]any)
		items, _ := params["items"].([]any)
		return make([]any, len(items))
	case "workspace/workspaceFolders":
		return []message{{"uri": c.rootURI, "name": c.rootName}}
	case "client/registerCapability",
		"client/unregisterCapability",
		"window/workDoneProgress/create",
		"window/showMessageRequest":
		return nil
	default:
		return nil
	}
}

func (c *lspClient) printMessage(direction string, payload message) {
	if !c.trace {
		return
	}

	c.printMu.Lock()
	defer c.printMu.Unlock()

	label := "response"
	if method, ok := payload["method"].(string); ok {
		label = method
	}
	if id, ok := payload["id"]; ok {
		label = fmt.Sprintf("%s#%s", label, idKey(id))
	}

	fmt.Printf("%s %s\n", direction, label)
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Printf("%v\n\n", payload)
		return
	}
	fmt.Printf("%s\n\n", pretty)
}

func (c *lspClient) kill() {
	if c.cmd.Process != nil && c.cmd.ProcessState == nil {
		_ = c.cmd.Process.Kill()
	}
}

func collectDeclarations(result any) []*declaration {
	items, ok := result.([]any)
	if !ok {
		return nil
	}

	var declarations []*declaration
	var walk func([]any)
	walk = func(symbols []any) {
		for _, raw := range symbols {
			symbol, ok := raw.(map[string]any)
			if !ok {
				continue
			}

			decl, ok := declarationFromSymbol(symbol)
			if ok && isCandidateDeclaration(decl) {
				declarations = append(declarations, decl)
			}

			if children, ok := symbol["children"].([]any); ok {
				walk(children)
			}
		}
	}
	walk(items)

	sort.SliceStable(declarations, func(i, j int) bool {
		a := declarations[i].rng.start
		b := declarations[j].rng.start
		if a.line != b.line {
			return a.line < b.line
		}
		return a.character < b.character
	})

	return declarations
}

func declarationsByName(declarations []*declaration) map[string]*declaration {
	byName := map[string]*declaration{}
	for _, decl := range declarations {
		if _, exists := byName[decl.name]; !exists {
			byName[decl.name] = decl
		}
	}
	return byName
}

func findDeclaration(declarations []*declaration, name string) *declaration {
	if name == "" {
		return nil
	}
	if strings.Contains(name, ".") {
		name = name[strings.LastIndex(name, ".")+1:]
	}
	for _, decl := range declarations {
		if decl.name == name {
			return decl
		}
	}
	return nil
}

func declarationFromSymbol(symbol map[string]any) (*declaration, bool) {
	name, _ := symbol["name"].(string)
	if name == "" {
		return nil, false
	}

	kind, ok := intFromAny(symbol["kind"])
	if !ok {
		return nil, false
	}

	rng, ok := rangeFromAny(symbol["range"])
	if !ok {
		if location, ok := symbol["location"].(map[string]any); ok {
			rng, ok = rangeFromAny(location["range"])
		}
	}
	if !ok {
		return nil, false
	}

	selection := rng
	if selectionRange, ok := rangeFromAny(symbol["selectionRange"]); ok {
		selection = selectionRange
	}

	return &declaration{
		name:      name,
		kind:      kind,
		rng:       rng,
		selection: selection,
		component: isComponentName(name),
	}, true
}

func isCandidateDeclaration(decl *declaration) bool {
	if strings.Contains(decl.name, " callback") {
		return false
	}

	switch decl.kind {
	case symbolKindFunction, symbolKindMethod, symbolKindConstructor:
		return true
	case symbolKindVariable, symbolKindConstant:
		return isComponentName(decl.name)
	default:
		return false
	}
}

func filterSourceDeclarations(text string, declarations []*declaration) []*declaration {
	index := newSourceIndex(text)
	var filtered []*declaration
	for _, decl := range declarations {
		start := index.offsetAt(decl.rng.start)
		lineEnd := start
		for lineEnd < len(text) && text[lineEnd] != '\n' {
			lineEnd++
		}
		line := strings.TrimSpace(text[start:lineEnd])
		if strings.HasPrefix(line, "type ") || strings.HasPrefix(line, "interface ") {
			continue
		}
		filtered = append(filtered, decl)
	}
	return filtered
}

func parseImports(text string, documentPath string) map[string]string {
	imports := map[string]string{}
	statements := collectImportStatements(text)

	for _, stmt := range statements {
		modulePath := importModulePath(stmt)
		if modulePath == "" {
			continue
		}
		resolved := resolveImportPath(filepath.Dir(documentPath), modulePath)
		if resolved == "" {
			continue
		}

		for _, local := range importLocalNames(stmt) {
			imports[local] = resolved
		}
	}

	return imports
}

func collectImportStatements(text string) []string {
	var statements []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "import ") {
			continue
		}

		statement := line
		for !strings.Contains(statement, " from ") && !strings.Contains(statement, ";") && i+1 < len(lines) {
			i++
			statement += " " + strings.TrimSpace(lines[i])
		}
		for !strings.Contains(statement, ";") && i+1 < len(lines) && strings.Count(statement, "{") > strings.Count(statement, "}") {
			i++
			statement += " " + strings.TrimSpace(lines[i])
		}
		statements = append(statements, statement)
	}
	return statements
}

func importModulePath(statement string) string {
	idx := strings.LastIndex(statement, " from ")
	if idx == -1 {
		fields := strings.Fields(statement)
		if len(fields) >= 2 {
			return strings.Trim(fields[len(fields)-1], `"' ;`)
		}
		return ""
	}
	return strings.Trim(statement[idx+len(" from "):], `"' ;`)
}

func importLocalNames(statement string) []string {
	beforeFrom := statement
	if idx := strings.LastIndex(statement, " from "); idx != -1 {
		beforeFrom = statement[:idx]
	}
	beforeFrom = strings.TrimSpace(strings.TrimPrefix(beforeFrom, "import"))

	var names []string
	if strings.HasPrefix(beforeFrom, "{") {
		return namedImportLocalNames(beforeFrom)
	}

	if strings.Contains(beforeFrom, ",") {
		parts := strings.SplitN(beforeFrom, ",", 2)
		defaultName := strings.TrimSpace(parts[0])
		if isLikelyIdentifier(defaultName) {
			names = append(names, defaultName)
		}
		names = append(names, namedImportLocalNames(parts[1])...)
		return names
	}

	if strings.HasPrefix(beforeFrom, "* as ") {
		local := strings.TrimSpace(strings.TrimPrefix(beforeFrom, "* as "))
		if isLikelyIdentifier(local) {
			return []string{local}
		}
		return nil
	}

	if isLikelyIdentifier(beforeFrom) {
		return []string{beforeFrom}
	}

	return nil
}

func namedImportLocalNames(segment string) []string {
	start := strings.Index(segment, "{")
	end := strings.LastIndex(segment, "}")
	if start == -1 || end == -1 || start >= end {
		return nil
	}

	body := segment[start+1 : end]
	var names []string
	for _, part := range strings.Split(body, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		fields := strings.Fields(item)
		local := fields[len(fields)-1]
		if isLikelyIdentifier(local) {
			names = append(names, local)
		}
	}
	return names
}

func resolveImportPath(baseDir string, modulePath string) string {
	if !strings.HasPrefix(modulePath, ".") {
		return ""
	}

	base := filepath.Clean(filepath.Join(baseDir, modulePath))
	candidates := []string{
		base,
		base + ".tsx",
		base + ".ts",
		base + ".jsx",
		base + ".js",
		filepath.Join(base, "index.tsx"),
		filepath.Join(base, "index.ts"),
		filepath.Join(base, "index.jsx"),
		filepath.Join(base, "index.js"),
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

func isLikelyIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 && !isIdentStart(r) {
			return false
		}
		if i > 0 && !isIdentPart(r) {
			return false
		}
	}
	return true
}

func assignBodyRanges(text string, declarations []*declaration) {
	index := newSourceIndex(text)
	for _, decl := range declarations {
		rangeStart := index.offsetAt(decl.rng.start)
		rangeEnd := index.offsetAt(decl.rng.end)
		selectionEnd := index.offsetAt(decl.selection.end)

		decl.bodyStart = rangeStart
		decl.bodyEnd = rangeEnd

		open := findOpeningBodyBrace(text, selectionEnd, rangeEnd)
		if open == -1 {
			continue
		}

		close := findMatchingBrace(text, open, rangeEnd)
		if close == -1 {
			decl.bodyStart = open + 1
			decl.bodyEnd = rangeEnd
			continue
		}

		decl.bodyStart = open + 1
		decl.bodyEnd = close
	}
}

func buildTree(text string, declarations []*declaration) []*declaration {
	code := maskNonCode(text)

	byName := map[string]*declaration{}
	for _, decl := range declarations {
		if _, exists := byName[decl.name]; !exists {
			byName[decl.name] = decl
		}
	}

	called := map[string]bool{}
	for _, decl := range declarations {
		start := decl.bodyStart
		end := decl.bodyEnd
		if start < 0 || end > len(code) || start >= end {
			continue
		}

		codeBody := code[start:end]
		sourceBody := text[start:end]
		decl.jsxRoots = scanJSXTree(sourceBody)

		var jsxNames []string
		collectJSXNames(decl.jsxRoots, &jsxNames)
		names := orderedUnique(append(scanCalls(codeBody), jsxNames...))
		for _, name := range names {
			target := byName[name]
			if target == nil || target.name == decl.name {
				continue
			}
			decl.calls = append(decl.calls, target.name)
			called[target.name] = true
		}
	}

	var roots []*declaration
	for _, decl := range declarations {
		if called[decl.name] {
			continue
		}
		if len(decl.calls) == 0 && !decl.component && !isFunctionLike(decl.kind) {
			continue
		}
		roots = append(roots, decl)
	}

	if len(roots) == 0 {
		for _, decl := range declarations {
			if len(decl.calls) > 0 || decl.component || isFunctionLike(decl.kind) {
				roots = append(roots, decl)
			}
		}
	}

	return roots
}

func printTree(analysis *fileAnalysis, analyzer *analyzer, maxDepth int) {
	fmt.Printf("Call/component tree: %s\n", analysis.path)
	if len(analysis.roots) == 0 {
		fmt.Println("(empty)")
		return
	}

	for _, root := range analysis.roots {
		fmt.Println(nodeLabelForContext(root.name, nil, analysis.path, analysis.path))
		printChildren(root, "", analysis, analyzer, maxDepth, 1, map[string]bool{root.name: true}, map[string]bool{analysis.path: true}, analysis.path)
	}
}

func printChildren(decl *declaration, prefix string, analysis *fileAnalysis, analyzer *analyzer, maxDepth int, depth int, seen map[string]bool, seenFiles map[string]bool, currentFile string) {
	byName := declarationsByName(analysis.declarations)
	children := declarationChildren(decl, byName)
	for i, child := range children {
		last := i == len(children)-1
		connector := "|- "
		nextPrefix := prefix + "|  "
		if last {
			connector = "`- "
			nextPrefix = prefix + "   "
		}

		if child.decl != nil && seen[child.name] {
			fmt.Printf("%s%s%s (cycle)\n", prefix, connector, nodeLabelForContext(child.name, child.props(), analysis.path, currentFile))
			continue
		}

		childFile := filePathForChild(child, analysis)
		fmt.Printf("%s%s%s\n", prefix, connector, nodeLabelForContext(child.name, child.props(), childFile, currentFile))
		if child.decl == nil {
			printExternalExpansion(child.name, nextPrefix, analysis, analyzer, maxDepth, depth, seenFiles, childFile)
			printJSXChildren(child.jsx.children, nextPrefix, analysis, analyzer, maxDepth, depth, seen, seenFiles, childFile)
			continue
		}

		nextSeen := copySeen(seen)
		nextSeen[child.decl.name] = true
		printChildren(child.decl, nextPrefix, analysis, analyzer, maxDepth, depth, nextSeen, seenFiles, childFile)
	}
}

type printableChild struct {
	name string
	decl *declaration
	jsx  *treeNode
}

func declarationChildren(decl *declaration, byName map[string]*declaration) []printableChild {
	var children []printableChild
	seen := map[string]bool{}
	for _, childName := range decl.calls {
		child := byName[childName]
		if child == nil || seen[child.name] {
			continue
		}
		seen[child.name] = true
		children = append(children, printableChild{name: child.name, decl: child})
	}

	for _, jsx := range decl.jsxRoots {
		addJSXPrintable(jsx, byName, seen, &children)
	}
	return children
}

func addJSXPrintable(jsx *treeNode, byName map[string]*declaration, seen map[string]bool, children *[]printableChild) {
	if jsx == nil {
		return
	}

	if child := byName[jsx.name]; child != nil {
		if !seen[child.name] {
			seen[child.name] = true
			*children = append(*children, printableChild{name: child.name, decl: child, jsx: jsx})
		}
		return
	}

	if !seen[jsx.name] {
		seen[jsx.name] = true
		*children = append(*children, printableChild{name: jsx.name, jsx: jsx})
	}
}

func (c printableChild) props() []propValue {
	if c.jsx == nil {
		return nil
	}
	return c.jsx.props
}

func printJSXChildren(nodes []*treeNode, prefix string, analysis *fileAnalysis, analyzer *analyzer, maxDepth int, depth int, seen map[string]bool, seenFiles map[string]bool, currentFile string) {
	byName := declarationsByName(analysis.declarations)
	for i, node := range nodes {
		last := i == len(nodes)-1
		connector := "|- "
		nextPrefix := prefix + "|  "
		if last {
			connector = "`- "
			nextPrefix = prefix + "   "
		}

		if decl := byName[node.name]; decl != nil {
			nodeFile := analysis.path
			fmt.Printf("%s%s%s\n", prefix, connector, nodeLabelForContext(decl.name, node.props, nodeFile, currentFile))
			if seen[decl.name] {
				continue
			}
			nextSeen := copySeen(seen)
			nextSeen[decl.name] = true
			printChildren(decl, nextPrefix, analysis, analyzer, maxDepth, depth, nextSeen, seenFiles, nodeFile)
			continue
		}

		nodeFile := filePathForName(node.name, analysis)
		fmt.Printf("%s%s%s\n", prefix, connector, nodeLabelForContext(node.name, node.props, nodeFile, currentFile))
		printExternalExpansion(node.name, nextPrefix, analysis, analyzer, maxDepth, depth, seenFiles, nodeFile)
		printJSXChildren(node.children, nextPrefix, analysis, analyzer, maxDepth, depth, seen, seenFiles, nodeFile)
	}
}

func printExternalExpansion(name string, prefix string, analysis *fileAnalysis, analyzer *analyzer, maxDepth int, depth int, seenFiles map[string]bool, currentFile string) {
	if depth >= maxDepth {
		return
	}

	importedPath := analysis.imports[name]
	if importedPath == "" && strings.Contains(name, ".") {
		importedPath = analysis.imports[strings.Split(name, ".")[0]]
	}
	if importedPath == "" || seenFiles[importedPath] {
		return
	}

	imported, err := analyzer.analyzeFile(importedPath)
	if err != nil || len(imported.roots) == 0 {
		return
	}

	nextSeenFiles := copySeen(seenFiles)
	nextSeenFiles[imported.path] = true

	root := findDeclaration(imported.declarations, name)
	if root == nil && strings.Contains(name, ".") {
		root = findDeclaration(imported.declarations, strings.TrimPrefix(name[strings.LastIndex(name, "."):], "."))
	}
	if root != nil {
		printChildren(root, prefix, imported, analyzer, maxDepth, depth+1, map[string]bool{root.name: true}, nextSeenFiles, imported.path)
		return
	}

	for i, importedRoot := range imported.roots {
		last := i == len(imported.roots)-1
		connector := "|- "
		nextPrefix := prefix + "|  "
		if last {
			connector = "`- "
			nextPrefix = prefix + "   "
		}
		fmt.Printf("%s%s%s\n", prefix, connector, nodeLabelForContext(importedRoot.name, nil, imported.path, currentFile))
		printChildren(importedRoot, nextPrefix, imported, analyzer, maxDepth, depth+1, map[string]bool{importedRoot.name: true}, nextSeenFiles, imported.path)
	}
}

func nodeLabel(name string, props []propValue, filePath string) string {
	label := name + propsSuffix(props)
	if filePath == "" {
		return label
	}
	return fmt.Sprintf("%s [file=%s]", label, filePath)
}

func nodeLabelForContext(name string, props []propValue, filePath string, currentFile string) string {
	if filePath == currentFile {
		return nodeLabel(name, props, "")
	}
	return nodeLabel(name, props, filePath)
}

func filePathForChild(child printableChild, analysis *fileAnalysis) string {
	if child.decl != nil {
		return analysis.path
	}
	return filePathForName(child.name, analysis)
}

func filePathForName(name string, analysis *fileAnalysis) string {
	if path := importedPathForName(name, analysis); path != "" {
		return path
	}
	if analysis == nil {
		return ""
	}
	return analysis.path
}

func importedPathForName(name string, analysis *fileAnalysis) string {
	if analysis == nil {
		return ""
	}
	importedPath := analysis.imports[name]
	if importedPath == "" && strings.Contains(name, ".") {
		importedPath = analysis.imports[strings.Split(name, ".")[0]]
	}
	return importedPath
}

func printJSONTree(analysis *fileAnalysis, analyzer *analyzer, maxDepth int) error {
	output := jsonTreeOutput{File: analysis.path}
	for _, root := range analysis.roots {
		output.Roots = append(output.Roots, buildDeclarationJSON(
			root,
			analysis,
			analyzer,
			maxDepth,
			1,
			map[string]bool{root.name: true},
			map[string]bool{analysis.path: true},
		))
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func buildDeclarationJSON(decl *declaration, analysis *fileAnalysis, analyzer *analyzer, maxDepth int, depth int, seen map[string]bool, seenFiles map[string]bool) jsonTreeNode {
	node := jsonTreeNode{
		Name: decl.name,
		Kind: declarationKindName(decl),
		File: analysis.path,
	}

	byName := declarationsByName(analysis.declarations)
	for _, child := range declarationChildren(decl, byName) {
		if child.decl != nil {
			if seen[child.name] {
				node.Children = append(node.Children, jsonTreeNode{
					Name:  child.name,
					Kind:  declarationKindName(child.decl),
					File:  analysis.path,
					Props: child.props(),
					Cycle: true,
				})
				continue
			}

			nextSeen := copySeen(seen)
			nextSeen[child.decl.name] = true
			childNode := buildDeclarationJSON(child.decl, analysis, analyzer, maxDepth, depth, nextSeen, seenFiles)
			childNode.Props = child.props()
			node.Children = append(node.Children, childNode)
			continue
		}

		jsxNode := jsonTreeNode{Name: child.name, Kind: "jsx", Props: child.props()}
		externalChildren, externalFile := externalExpansionJSON(child.name, analysis, analyzer, maxDepth, depth, seenFiles)
		if externalFile != "" {
			jsxNode.File = externalFile
		}
		jsxNode.Children = append(jsxNode.Children, externalChildren...)
		jsxNode.Children = append(jsxNode.Children, buildJSXChildrenJSON(child.jsx.children, analysis, analyzer, maxDepth, depth, seen, seenFiles)...)
		node.Children = append(node.Children, jsxNode)
	}

	return node
}

func buildJSXChildrenJSON(nodes []*treeNode, analysis *fileAnalysis, analyzer *analyzer, maxDepth int, depth int, seen map[string]bool, seenFiles map[string]bool) []jsonTreeNode {
	byName := declarationsByName(analysis.declarations)
	var children []jsonTreeNode
	for _, jsx := range nodes {
		if decl := byName[jsx.name]; decl != nil {
			if seen[decl.name] {
				children = append(children, jsonTreeNode{
					Name:  decl.name,
					Kind:  declarationKindName(decl),
					File:  analysis.path,
					Props: jsx.props,
					Cycle: true,
				})
				continue
			}

			nextSeen := copySeen(seen)
			nextSeen[decl.name] = true
			node := buildDeclarationJSON(decl, analysis, analyzer, maxDepth, depth, nextSeen, seenFiles)
			node.Props = jsx.props
			children = append(children, node)
			continue
		}

		node := jsonTreeNode{Name: jsx.name, Kind: "jsx", Props: jsx.props}
		externalChildren, externalFile := externalExpansionJSON(jsx.name, analysis, analyzer, maxDepth, depth, seenFiles)
		if externalFile != "" {
			node.File = externalFile
		}
		node.Children = append(node.Children, externalChildren...)
		node.Children = append(node.Children, buildJSXChildrenJSON(jsx.children, analysis, analyzer, maxDepth, depth, seen, seenFiles)...)
		children = append(children, node)
	}
	return children
}

func externalExpansionJSON(name string, analysis *fileAnalysis, analyzer *analyzer, maxDepth int, depth int, seenFiles map[string]bool) ([]jsonTreeNode, string) {
	if depth >= maxDepth {
		return nil, ""
	}

	importedPath := analysis.imports[name]
	if importedPath == "" && strings.Contains(name, ".") {
		importedPath = analysis.imports[strings.Split(name, ".")[0]]
	}
	if importedPath == "" || seenFiles[importedPath] {
		return nil, ""
	}

	imported, err := analyzer.analyzeFile(importedPath)
	if err != nil || len(imported.roots) == 0 {
		return nil, importedPath
	}

	nextSeenFiles := copySeen(seenFiles)
	nextSeenFiles[imported.path] = true

	root := findDeclaration(imported.declarations, name)
	if root == nil && strings.Contains(name, ".") {
		root = findDeclaration(imported.declarations, name[strings.LastIndex(name, ".")+1:])
	}
	if root != nil {
		expanded := buildDeclarationJSON(root, imported, analyzer, maxDepth, depth+1, map[string]bool{root.name: true}, nextSeenFiles)
		return expanded.Children, imported.path
	}

	var children []jsonTreeNode
	for _, importedRoot := range imported.roots {
		children = append(children, buildDeclarationJSON(
			importedRoot,
			imported,
			analyzer,
			maxDepth,
			depth+1,
			map[string]bool{importedRoot.name: true},
			nextSeenFiles,
		))
	}
	return children, imported.path
}

func declarationKindName(decl *declaration) string {
	if decl.component {
		return "component"
	}
	switch decl.kind {
	case symbolKindMethod:
		return "method"
	case symbolKindConstructor:
		return "constructor"
	case symbolKindFunction:
		return "function"
	case symbolKindVariable, symbolKindConstant:
		return "variable"
	default:
		return "declaration"
	}
}

func readMessage(reader *bufio.Reader) (message, error) {
	contentLength := -1

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		if line == "\r\n" || line == "\n" {
			break
		}

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = parsed
		}
	}

	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var msg message
	if err := decoder.Decode(&msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func newSourceIndex(text string) *sourceIndex {
	lineStart := []int{0}
	for i, r := range text {
		if r == '\n' {
			lineStart = append(lineStart, i+1)
		}
	}
	return &sourceIndex{text: text, lineStart: lineStart}
}

func (s *sourceIndex) offsetAt(pos position) int {
	if pos.line < 0 {
		return 0
	}
	if pos.line >= len(s.lineStart) {
		return len(s.text)
	}

	start := s.lineStart[pos.line]
	end := len(s.text)
	if pos.line+1 < len(s.lineStart) {
		end = s.lineStart[pos.line+1]
	}

	units := 0
	for offset := start; offset < end; {
		r, size := utf8.DecodeRuneInString(s.text[offset:end])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if units >= pos.character {
			return offset
		}
		if r > 0xffff {
			units += 2
		} else {
			units++
		}
		offset += size
	}

	return end
}

func maskNonCode(text string) string {
	bytes := []byte(text)
	for i := 0; i < len(bytes); i++ {
		switch bytes[i] {
		case '"', '\'', '`':
			quote := bytes[i]
			i++
			for i < len(bytes) {
				if bytes[i] == '\n' {
					i++
					continue
				}
				if bytes[i] == '\\' {
					bytes[i] = ' '
					if i+1 < len(bytes) {
						i++
						if bytes[i] != '\n' {
							bytes[i] = ' '
						}
					}
					i++
					continue
				}
				if bytes[i] == quote {
					break
				}
				bytes[i] = ' '
				i++
			}
		case '/':
			if i+1 >= len(bytes) {
				continue
			}
			if bytes[i+1] == '/' {
				bytes[i] = ' '
				bytes[i+1] = ' '
				i += 2
				for i < len(bytes) && bytes[i] != '\n' {
					bytes[i] = ' '
					i++
				}
			} else if bytes[i+1] == '*' {
				bytes[i] = ' '
				bytes[i+1] = ' '
				i += 2
				for i+1 < len(bytes) {
					if bytes[i] == '*' && bytes[i+1] == '/' {
						bytes[i] = ' '
						bytes[i+1] = ' '
						i++
						break
					}
					if bytes[i] != '\n' {
						bytes[i] = ' '
					}
					i++
				}
			}
		}
	}
	return string(bytes)
}

func scanCalls(code string) []string {
	var names []string
	for i := 0; i < len(code); {
		r, size := utf8.DecodeRuneInString(code[i:])
		if !isIdentStart(r) {
			i += size
			continue
		}

		start := i
		i += size
		for i < len(code) {
			r, size = utf8.DecodeRuneInString(code[i:])
			if !isIdentPart(r) {
				break
			}
			i += size
		}

		name := code[start:i]
		next := skipSpaces(code, i)
		if next >= len(code) || code[next] != '(' || isCallKeyword(name) || precededByFunctionKeyword(code, start) {
			continue
		}
		names = append(names, name)
	}
	return names
}

func scanJSXTree(code string) []*treeNode {
	var roots []*treeNode
	var stack []*treeNode

	for i := 0; i < len(code)-1; i++ {
		if code[i] != '<' {
			continue
		}

		if code[i+1] == '/' {
			name, ok := parseClosingJSXName(code, i+2)
			if ok {
				stack = popJSXStack(stack, name)
			}
			continue
		}

		name, nameEnd, ok := parseOpeningJSXName(code, i+1)
		if !ok {
			continue
		}

		tagEnd, selfClosing := findJSXTagEnd(code, nameEnd)
		if tagEnd == -1 {
			continue
		}

		node := &treeNode{name: name, props: parseJSXProps(code[nameEnd:tagEnd])}
		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node)
		}

		if !selfClosing {
			stack = append(stack, node)
		}
		i = tagEnd
	}

	return roots
}

func collectJSXNames(nodes []*treeNode, names *[]string) {
	for _, node := range nodes {
		*names = append(*names, node.name)
		collectJSXNames(node.children, names)
	}
}

func parseJSXProps(attrs string) []propValue {
	var props []propValue
	for i := 0; i < len(attrs); {
		i = skipSpaces(attrs, i)
		if i >= len(attrs) {
			break
		}
		if attrs[i] == '/' {
			i++
			continue
		}

		if attrs[i] == '{' {
			expr, next, ok := readBraceExpression(attrs, i)
			if ok {
				expr = strings.TrimSpace(expr)
				if strings.HasPrefix(expr, "...") {
					props = append(props, propValue{Name: "...spread", Value: strings.TrimSpace(strings.TrimPrefix(expr, "..."))})
				}
				i = next
				continue
			}
		}

		nameStart := i
		for i < len(attrs) {
			r, size := utf8.DecodeRuneInString(attrs[i:])
			if !isPropNamePart(r) {
				break
			}
			i += size
		}
		if nameStart == i {
			_, size := utf8.DecodeRuneInString(attrs[i:])
			i += size
			continue
		}

		name := attrs[nameStart:i]
		i = skipSpaces(attrs, i)
		if i >= len(attrs) || attrs[i] != '=' {
			props = append(props, propValue{Name: name, Value: "true"})
			continue
		}

		i++
		i = skipSpaces(attrs, i)
		value, next := readJSXPropValue(attrs, i)
		props = append(props, propValue{Name: name, Value: value})
		i = next
	}
	return props
}

func readJSXPropValue(attrs string, start int) (string, int) {
	if start >= len(attrs) {
		return "", start
	}

	switch attrs[start] {
	case '"', '\'':
		quote := attrs[start]
		i := start + 1
		for i < len(attrs) {
			if attrs[i] == '\\' {
				i += 2
				continue
			}
			if attrs[i] == quote {
				return attrs[start+1 : i], i + 1
			}
			i++
		}
		return attrs[start+1:], len(attrs)
	case '{':
		if expr, next, ok := readBraceExpression(attrs, start); ok {
			return strings.TrimSpace(expr), next
		}
	}

	i := start
	for i < len(attrs) && !unicode.IsSpace(rune(attrs[i])) && attrs[i] != '/' {
		i++
	}
	return attrs[start:i], i
}

func readBraceExpression(text string, start int) (string, int, bool) {
	if start >= len(text) || text[start] != '{' {
		return "", start, false
	}

	depth := 0
	for i := start; i < len(text); {
		next, ok := skipStringOrComment(text, i, len(text))
		if ok {
			i = next
			continue
		}

		r, size := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start+1 : i], i + size, true
			}
		}
		i += size
	}
	return text[start+1:], len(text), false
}

func parseOpeningJSXName(code string, start int) (string, int, bool) {
	if start >= len(code) || code[start] == '>' || code[start] == '!' || code[start] == '?' {
		return "", start, false
	}

	r, size := utf8.DecodeRuneInString(code[start:])
	if !unicode.IsUpper(r) {
		return "", start, false
	}

	nameEnd := start + size
	for nameEnd < len(code) {
		r, size = utf8.DecodeRuneInString(code[nameEnd:])
		if !(isIdentPart(r) || r == '.') {
			break
		}
		nameEnd += size
	}

	return code[start:nameEnd], nameEnd, true
}

func parseClosingJSXName(code string, start int) (string, bool) {
	if start >= len(code) {
		return "", false
	}

	r, size := utf8.DecodeRuneInString(code[start:])
	if !unicode.IsUpper(r) {
		return "", false
	}

	nameEnd := start + size
	for nameEnd < len(code) {
		r, size = utf8.DecodeRuneInString(code[nameEnd:])
		if !(isIdentPart(r) || r == '.') {
			break
		}
		nameEnd += size
	}

	return code[start:nameEnd], true
}

func findJSXTagEnd(code string, start int) (int, bool) {
	quote := rune(0)
	braceDepth := 0
	for i := start; i < len(code); {
		r, size := utf8.DecodeRuneInString(code[i:])
		if quote != 0 {
			if r == '\\' {
				i += size
				if i < len(code) {
					_, nextSize := utf8.DecodeRuneInString(code[i:])
					i += nextSize
				}
				continue
			}
			if r == quote {
				quote = 0
			}
			i += size
			continue
		}

		switch r {
		case '"', '\'', '`':
			quote = r
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '>':
			if braceDepth == 0 {
				j := i - 1
				for j >= start && unicode.IsSpace(rune(code[j])) {
					j--
				}
				return i, j >= start && code[j] == '/'
			}
		}
		i += size
	}
	return -1, false
}

func popJSXStack(stack []*treeNode, name string) []*treeNode {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].name == name {
			return stack[:i]
		}
	}
	return stack
}

func orderedUnique(names []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result
}

func propsSuffix(props []propValue) string {
	if len(props) == 0 {
		return ""
	}

	parts := make([]string, 0, len(props))
	for _, prop := range props {
		if prop.Value == "" || prop.Value == "true" {
			parts = append(parts, prop.Name)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s={%s}", prop.Name, compactPropValue(prop.Value)))
	}
	return " [" + strings.Join(parts, ", ") + "]"
}

func compactPropValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 80 {
		return value[:77] + "..."
	}
	return value
}

func skipSpaces(text string, i int) int {
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsSpace(r) {
			return i
		}
		i += size
	}
	return i
}

func findOpeningBodyBrace(text string, start int, end int) int {
	parenDepth := 0
	bracketDepth := 0
	for i := start; i < end; {
		next, ok := skipStringOrComment(text, i, end)
		if ok {
			i = next
			continue
		}

		r, size := utf8.DecodeRuneInString(text[i:end])
		switch r {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			if parenDepth == 0 && bracketDepth == 0 {
				return i
			}
		}
		i += size
	}
	return -1
}

func findMatchingBrace(text string, open int, end int) int {
	depth := 0
	for i := open; i < end; {
		next, ok := skipStringOrComment(text, i, end)
		if ok {
			i = next
			continue
		}

		r, size := utf8.DecodeRuneInString(text[i:end])
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i += size
	}
	return -1
}

func skipStringOrComment(text string, start int, end int) (int, bool) {
	if start >= end {
		return start, false
	}

	r, size := utf8.DecodeRuneInString(text[start:end])
	if r == '"' || r == '\'' || r == '`' {
		quote := r
		i := start + size
		for i < end {
			r, size = utf8.DecodeRuneInString(text[i:end])
			if r == '\\' {
				i += size
				if i < end {
					_, nextSize := utf8.DecodeRuneInString(text[i:end])
					i += nextSize
				}
				continue
			}
			i += size
			if r == quote {
				return i, true
			}
		}
		return end, true
	}

	if text[start] == '/' && start+1 < end {
		switch text[start+1] {
		case '/':
			i := start + 2
			for i < end && text[i] != '\n' {
				i++
			}
			return i, true
		case '*':
			i := start + 2
			for i+1 < end {
				if text[i] == '*' && text[i+1] == '/' {
					return i + 2, true
				}
				i++
			}
			return end, true
		}
	}

	return start, false
}

func precededByFunctionKeyword(code string, start int) bool {
	before := strings.TrimRightFunc(code[:start], unicode.IsSpace)
	if !strings.HasSuffix(before, "function") {
		return false
	}
	if len(before) == len("function") {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(before[:len(before)-len("function")])
	return !isIdentPart(r)
}

func isCallKeyword(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "catch", "function", "return", "typeof", "new":
		return true
	default:
		return false
	}
}

func isFunctionLike(kind int) bool {
	return kind == symbolKindFunction || kind == symbolKindMethod || kind == symbolKindConstructor
}

func isComponentName(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

func isPropNamePart(r rune) bool {
	return isIdentPart(r) || r == '-' || r == ':'
}

func rangeFromAny(value any) (rangeValue, bool) {
	raw, ok := value.(map[string]any)
	if !ok {
		return rangeValue{}, false
	}

	start, ok := positionFromAny(raw["start"])
	if !ok {
		return rangeValue{}, false
	}

	end, ok := positionFromAny(raw["end"])
	if !ok {
		return rangeValue{}, false
	}

	return rangeValue{start: start, end: end}, true
}

func positionFromAny(value any) (position, bool) {
	raw, ok := value.(map[string]any)
	if !ok {
		return position{}, false
	}

	line, ok := intFromAny(raw["line"])
	if !ok {
		return position{}, false
	}

	character, ok := intFromAny(raw["character"])
	if !ok {
		return position{}, false
	}

	return position{line: line, character: character}, true
}

func intFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		i, err := typed.Int64()
		return int(i), err == nil
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func copySeen(input map[string]bool) map[string]bool {
	output := map[string]bool{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func printStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "[server stderr] %s\n", scanner.Text())
	}
}

func idKey(id any) string {
	switch value := id.(type) {
	case json.Number:
		return value.String()
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

func fileURI(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
}

func languageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	default:
		return "typescript"
	}
}

func serverBinaryName() string {
	if runtime.GOOS == "windows" {
		return "typescript-language-server.cmd"
	}
	return "typescript-language-server"
}
