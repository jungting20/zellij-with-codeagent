# Runtime Dashboard Usability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `zellij-agent dashboard` easier to scan, navigate, scroll, and operate while preserving its transport-only runtime boundary and existing actions.

**Architecture:** Add a small viewport primitive and presentation-only focus/tab state to `internal/dashboard.Model`. Recompose the current view into a health header, focused runtime/detail panels, contextual footer, and modal overlays; the daemon API and `Client` interface remain unchanged.

**Tech Stack:** Go 1.26, Bubble Tea v1.3.10, Lip Gloss v1.1.0, `charmbracelet/x/ansi`, Go standard `testing` package.

## Global Constraints

- Every runtime read and mutation continues through the existing dashboard `Client`; do not import `internal/zellij`.
- Keep the hierarchy exactly `session -> task -> tab -> pane`.
- Keep input limited to `starting` and `running` panes and cleanup scoped to the selected pane's non-empty task ID.
- Preserve the existing refresh coalescing, event-stream degradation, action duplicate suppression, and queued snapshot behavior.
- Exclude `raw_output` from the semantic event list.
- Use text and symbols in addition to color for status, focus, and errors.
- Use the wide split layout at widths `>= 90`; use a single focused panel below 90 columns.
- Rendering must fit the configured width and height and must not panic at zero dimensions.
- Use TDD for each task: observe focused tests fail before implementation, then make them pass.

---

## File Structure

- Create `internal/dashboard/viewport.go`: vertical viewport offset, follow-bottom behavior, visible slicing, and position labels.
- Create `internal/dashboard/viewport_test.go`: deterministic viewport behavior tests.
- Modify `internal/dashboard/model.go`: pane cache, load timestamp, focus/tab state, viewport updates, and focused navigation.
- Modify `internal/dashboard/model_test.go`: focus, tab, scroll, refresh preservation, and snapshot-follow tests.
- Modify `internal/dashboard/actions.go`: help mode routing while preserving existing action modes.
- Replace the composition in `internal/dashboard/view.go`: header, panels, responsive layout, footer, status symbols, and overlays.
- Expand `internal/dashboard/view_test.go`: wide/narrow/tiny layouts, lifecycle summary, focused panels, tabs, empty/loading/error states, and overlays.
- Modify `docs/manual-smoke-test.md`: real-Zellij usability flow.

### Task 1: Add a Deterministic Vertical Viewport

**Files:**
- Create: `internal/dashboard/viewport.go`
- Create: `internal/dashboard/viewport_test.go`

**Interfaces:**
- Consumes: plain `[]string` content and a non-negative body height.
- Produces: `type viewport`, `newViewport() viewport`, `(*viewport).setContent(int, int)`, `(*viewport).scroll(int, int, int)`, `(*viewport).ensureVisible(int, int, int)`, `(*viewport).top()`, `(*viewport).bottom(int, int)`, `viewport.visible([]string, int) []string`, and `viewport.position(int, int) string`.

- [ ] **Step 1: Write failing viewport tests**

```go
package dashboard

import (
    "reflect"
    "testing"
)

func TestViewportFollowsBottomAndPreservesReadingPosition(t *testing.T) {
    v := newViewport()
    v.setContent(6, 3)
    if v.offset != 3 || !v.followBottom {
        t.Fatalf("initial viewport = %#v, want bottom offset 3", v)
    }
    v.scroll(-1, 6, 3)
    if v.offset != 2 || v.followBottom {
        t.Fatalf("scrolled viewport = %#v, want offset 2 not following", v)
    }
    v.setContent(8, 3)
    if v.offset != 2 {
        t.Fatalf("grown content offset = %d, want 2", v.offset)
    }
    v.bottom(8, 3)
    v.setContent(10, 3)
    if v.offset != 7 || !v.followBottom {
        t.Fatalf("following viewport = %#v, want bottom offset 7", v)
    }
}

func TestViewportClampsSlicesAndReportsPosition(t *testing.T) {
    lines := []string{"0", "1", "2", "3", "4"}
    v := viewport{offset: 4}
    v.setContent(len(lines), 2)
    if got := v.visible(lines, 2); !reflect.DeepEqual(got, []string{"3", "4"}) {
        t.Fatalf("visible = %#v", got)
    }
    if got := v.position(len(lines), 2); got != "4-5/5" {
        t.Fatalf("position = %q, want 4-5/5", got)
    }
    v.top()
    v.scroll(20, len(lines), 2)
    if v.offset != 3 || !v.followBottom {
        t.Fatalf("clamped viewport = %#v", v)
    }
}

func TestViewportEnsuresSelectedRowIsVisible(t *testing.T) {
    v := viewport{}
    v.ensureVisible(5, 10, 3)
    if v.offset != 3 || v.followBottom {
        t.Fatalf("viewport = %#v, want offset 3", v)
    }
    v.ensureVisible(2, 10, 3)
    if v.offset != 2 {
        t.Fatalf("viewport offset = %d, want 2", v.offset)
    }
}
```

- [ ] **Step 2: Run the viewport tests and verify RED**

Run: `go test ./internal/dashboard -run '^TestViewport' -count=1`

Expected: compilation fails because `newViewport` and `viewport` do not exist.

- [ ] **Step 3: Implement the viewport primitive**

```go
package dashboard

import "fmt"

type viewport struct {
    offset       int
    followBottom bool
}

func newViewport() viewport { return viewport{followBottom: true} }

func viewportMax(total, height int) int {
    if total <= height || height <= 0 {
        return 0
    }
    return total - height
}

func (v *viewport) setContent(total, height int) {
    maximum := viewportMax(total, height)
    if v.followBottom {
        v.offset = maximum
    }
    if v.offset < 0 {
        v.offset = 0
    }
    if v.offset > maximum {
        v.offset = maximum
    }
    v.followBottom = v.offset == maximum
}

func (v *viewport) scroll(delta, total, height int) {
    v.offset += delta
    maximum := viewportMax(total, height)
    if v.offset < 0 {
        v.offset = 0
    }
    if v.offset > maximum {
        v.offset = maximum
    }
    v.followBottom = v.offset == maximum
}

func (v *viewport) ensureVisible(index, total, height int) {
    if height <= 0 || total <= 0 {
        v.offset = 0
        return
    }
    if index < v.offset {
        v.offset = index
    } else if index >= v.offset+height {
        v.offset = index - height + 1
    }
    maximum := viewportMax(total, height)
    if v.offset < 0 { v.offset = 0 }
    if v.offset > maximum { v.offset = maximum }
    v.followBottom = false
}

func (v *viewport) top() {
    v.offset = 0
    v.followBottom = false
}

func (v *viewport) bottom(total, height int) {
    v.offset = viewportMax(total, height)
    v.followBottom = true
}

func (v viewport) visible(lines []string, height int) []string {
    if height <= 0 || len(lines) == 0 {
        return nil
    }
    maximum := viewportMax(len(lines), height)
    start := v.offset
    if start < 0 {
        start = 0
    }
    if start > maximum {
        start = maximum
    }
    end := start + height
    if end > len(lines) {
        end = len(lines)
    }
    return lines[start:end]
}

func (v viewport) position(total, height int) string {
    if total == 0 {
        return "0/0"
    }
    start := v.offset
    maximum := viewportMax(total, height)
    if start > maximum {
        start = maximum
    }
    end := start + height
    if end > total {
        end = total
    }
    return fmt.Sprintf("%d-%d/%d", start+1, end, total)
}
```

- [ ] **Step 4: Format and verify GREEN**

Run: `gofmt -w internal/dashboard/viewport.go internal/dashboard/viewport_test.go && go test ./internal/dashboard -run '^TestViewport' -count=1`

Expected: `ok zellij-with-codeagent/internal/dashboard`.

- [ ] **Step 5: Commit the viewport**

```bash
git add internal/dashboard/viewport.go internal/dashboard/viewport_test.go
git commit -m "feat: add dashboard viewports"
```

### Task 2: Add Focused Navigation, Detail Tabs, and Refresh-Aware Viewport State

**Files:**
- Modify: `internal/dashboard/model.go:35-392`
- Modify: `internal/dashboard/model_test.go:69-366`

**Interfaces:**
- Consumes: Task 1 `viewport` methods and existing Bubble Tea key/update flow.
- Produces: `type panelFocus string`, `type detailTab string`, `Model.focus`, `Model.detailTab`, `Model.treeViewport`, `Model.outputViewport`, `Model.eventViewport`, `Model.panes`, `Model.loaded`, `Model.lastRefresh`, `Model.panelBodyHeight() int`, `Model.outputLines() []string`, and `Model.eventLines() []string`.

- [ ] **Step 1: Write failing focus, tab, and scrolling tests**

Append these tests to `internal/dashboard/model_test.go`:

```go
func TestModelFocusControlsNavigationAndDetailTab(t *testing.T) {
    _, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", Status: "running"})
    selected := m.selected
    next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
    m = next.(Model)
    if m.focus != focusDetail || m.selected != selected {
        t.Fatalf("tab model = %#v", m)
    }
    next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
    m = next.(Model)
    if m.detailTab != tabEvents {
        t.Fatalf("detail tab = %q, want events", m.detailTab)
    }
    m.events = []transport.Event{{Type: "one"}, {Type: "two"}, {Type: "three"}}
    m.height = 8
    next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
    if next.(Model).eventViewport.followBottom {
        t.Fatal("scrolling up must leave follow-bottom mode")
    }
}

func TestModelTreeKeysDoNotMoveSelectionWhenDetailFocused(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
        {ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
        {ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
    }})
    before := m.selected
    m.focus = focusDetail
    next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
    if next.(Model).selected != before {
        t.Fatalf("selection moved from %d to %d", before, next.(Model).selected)
    }
}

func TestModelTreeSelectionStaysInsideViewport(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    m.height = 9
    var panes []transport.Pane
    for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
        panes = append(panes, transport.Pane{ID: id, SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"})
    }
    m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: panes})
    for i := 0; i < len(m.rows); i++ {
        next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
        m = next.(Model)
    }
    height := m.panelBodyHeight()
    if m.selected < m.treeViewport.offset || m.selected >= m.treeViewport.offset+height {
        t.Fatalf("selected=%d viewport=%#v height=%d", m.selected, m.treeViewport, height)
    }
}

func TestModelRefreshPreservesPresentationState(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    m.focus, m.detailTab = focusDetail, tabEvents
    m.eventViewport = viewport{offset: 1, followBottom: false}
    m.refreshing = true
    at := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
    next, _ := m.Update(refreshResultMsg{
        status: transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"}}},
        events: transport.RecentEventsResponse{Events: []transport.Event{{Type: "one"}, {Type: "two"}, {Type: "three"}}},
        at: at,
    })
    got := next.(Model)
    if got.focus != focusDetail || got.detailTab != tabEvents || got.lastRefresh != at || !got.loaded {
        t.Fatalf("presentation state = %#v", got)
    }
}
```

- [ ] **Step 2: Run model tests and verify RED**

Run: `go test ./internal/dashboard -run '^(TestModelFocus|TestModelTreeKeys|TestModelRefreshPreserves)' -count=1`

Expected: compilation fails for undefined focus/tab fields and constants.

- [ ] **Step 3: Add presentation state and timestamped refresh messages**

Add these declarations and fields in `model.go`:

```go
type panelFocus string
type detailTab string

const (
    focusTree   panelFocus = "tree"
    focusDetail panelFocus = "detail"
    tabOutput   detailTab   = "output"
    tabEvents   detailTab   = "events"
)

type refreshResultMsg struct {
    status transport.InspectRuntimeResponse
    events transport.RecentEventsResponse
    at     time.Time
    err    error
}

// Add inside Model.
panes          []transport.Pane
focus          panelFocus
detailTab      detailTab
treeViewport   viewport
outputViewport viewport
eventViewport  viewport
loaded         bool
lastRefresh    time.Time
```

Initialize the fields in `NewModel`:

```go
focus:          focusTree,
detailTab:      tabOutput,
treeViewport:   viewport{},
outputViewport: newViewport(),
eventViewport:  newViewport(),
```

Return `at: time.Now()` from `refreshCmd`. On successful refresh, copy `msg.status.Panes` into `m.panes`, set `m.loaded = true`, use `msg.at` or `time.Now()` when it is zero, update events, clamp the tree viewport and keep the selected row visible, and call `m.eventViewport.setContent(len(m.events), m.panelBodyHeight())`. On snapshot success, update the output viewport only when the response pane still matches `m.selectedPane()`:

```go
m.snapshots[msg.paneID] = msg.output
if pane := m.selectedPane(); pane != nil && pane.ID == msg.paneID {
    m.outputViewport.setContent(len(m.outputLines()), m.panelBodyHeight())
}
```

- [ ] **Step 4: Implement focused navigation**

Add helpers to `model.go`:

```go
func (m Model) panelBodyHeight() int {
    height := m.height - 7
    if height < 1 {
        return 1
    }
    return height
}

func (m Model) outputLines() []string {
    pane := m.selectedPane()
    if pane == nil {
        return []string{"Select a pane to inspect output"}
    }
    output := m.snapshots[pane.ID]
    if output == "" {
        return []string{"No snapshot output"}
    }
    return strings.Split(strings.TrimSuffix(output, "\n"), "\n")
}

func (m Model) eventLines() []string {
    if len(m.events) == 0 {
        return []string{"No semantic events"}
    }
    lines := make([]string, 0, len(m.events))
    for _, event := range m.events {
        line := event.Type
        if event.PaneID != "" { line += " pane=" + event.PaneID }
        if event.Message != "" { line += " " + event.Message }
        lines = append(lines, line)
    }
    return lines
}

func (m Model) scrollDetail(delta int) Model {
    height := m.panelBodyHeight()
    if m.detailTab == tabEvents {
        m.eventViewport.scroll(delta, len(m.eventLines()), height)
    } else {
        m.outputViewport.scroll(delta, len(m.outputLines()), height)
    }
    return m
}
```

In `updateNormalKey`, handle `tab` globally, use `h`/left and `l`/right for tabs only under `focusDetail`, route `j`/`k` to `scrollDetail` under `focusDetail`, route page keys by `panelBodyHeight()-1`, and route `g`/`G` to the active detail viewport's `top`/`bottom`. Keep selection and `enter` tree-only. After selection movement, collapse, or refresh, call `m.treeViewport.ensureVisible(m.selected, len(m.rows), m.panelBodyHeight())`. In `moveSelection`, also reset `m.outputViewport = newViewport()` when the pane ID changes.

- [ ] **Step 5: Format and verify GREEN plus existing dashboard regression**

Run: `gofmt -w internal/dashboard/model.go internal/dashboard/model_test.go && go test ./internal/dashboard -count=1`

Expected: `ok zellij-with-codeagent/internal/dashboard`.

- [ ] **Step 6: Commit focused navigation**

```bash
git add internal/dashboard/model.go internal/dashboard/model_test.go
git commit -m "feat: add focused dashboard navigation"
```

### Task 3: Render the Operations Header and Responsive Focused Panels

**Files:**
- Modify: `internal/dashboard/view.go:11-178`
- Modify: `internal/dashboard/view_test.go:1-68`

**Interfaces:**
- Consumes: Task 2 model state, existing tree rows, and ANSI-aware `truncate`/`fitScreen` helpers.
- Produces: `Model.headerView()`, `Model.runtimePanel(int, int)`, `Model.detailPanel(int, int)`, `Model.footerView()`, `lifecycleSummary([]transport.Pane)`, `statusSymbol(string) string`, and the `>= 90` responsive split. `runtimePanel` consumes `Model.treeViewport` so long trees scroll while selection remains visible.

- [ ] **Step 1: Write failing responsive rendering tests**

Append to `view_test.go`:

```go
func TestViewShowsHealthSummaryFocusTabsAndSymbols(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    m.width, m.height, m.connection, m.loaded = 120, 24, "live", true
    m.refreshing = true
    next, _ := m.Update(refreshResultMsg{status: transport.InspectRuntimeResponse{Panes: []transport.Pane{
        {ID: "coder", SessionID: "s", TaskID: "t", TabID: "tab", Role: "coding-agent", Status: "running"},
        {ID: "tester", SessionID: "s", TaskID: "t", TabID: "tab", Role: "tester", Status: "error"},
    }}, at: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)})
    m = next.(Model)
    view := m.View()
    for _, want := range []string{"LIVE", "2 panes", "active=1", "problem=1", "RUNTIME [FOCUSED]", "[Output]", "Events", "* coder", "! tester"} {
        if !strings.Contains(view, want) { t.Fatalf("view missing %q: %q", want, view) }
    }
}

func TestViewNarrowLayoutShowsOnlyFocusedPanel(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    m.width, m.height, m.loaded = 60, 18, true
    m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "coder", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"}}})
    treeView := m.View()
    if !strings.Contains(treeView, "RUNTIME [FOCUSED]") || strings.Contains(treeView, "DETAIL [FOCUSED]") {
        t.Fatalf("tree-focused narrow view = %q", treeView)
    }
    m.focus = focusDetail
    detailView := m.View()
    if !strings.Contains(detailView, "DETAIL [FOCUSED]") || strings.Contains(detailView, "RUNTIME [FOCUSED]") {
        t.Fatalf("detail-focused narrow view = %q", detailView)
    }
}

func TestViewDistinguishesLoadingFromEmptyRuntime(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    m.width, m.height = 80, 16
    if view := m.View(); !strings.Contains(view, "Loading runtime") { t.Fatalf("loading view = %q", view) }
    m.loaded = true
    if view := m.View(); !strings.Contains(view, "No managed panes") || !strings.Contains(view, "Start a managed workspace") {
        t.Fatalf("empty view = %q", view)
    }
}
```

Add `time` to the test imports.

- [ ] **Step 2: Run view tests and verify RED**

Run: `go test ./internal/dashboard -run '^(TestViewShowsHealth|TestViewNarrow|TestViewDistinguishes)' -count=1`

Expected: assertions fail because the current renderer has no summary, focus marker, tabs, or loading state.

- [ ] **Step 3: Replace the top-level view composition**

Use this structure in `View`:

```go
func (m Model) View() string {
    width := m.width
    if width <= 0 { width = 80 }
    bodyHeight := m.height - 4
    if bodyHeight < 1 { bodyHeight = 1 }

    lines := []string{m.headerView(width)}
    if width >= 90 {
        leftWidth := maxInt(30, width*36/100)
        rightWidth := width - leftWidth - 1
        lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top,
            m.runtimePanel(leftWidth, bodyHeight), " ",
            m.detailPanel(rightWidth, bodyHeight)))
    } else if m.focus == focusDetail {
        lines = append(lines, m.detailPanel(width, bodyHeight))
    } else {
        lines = append(lines, m.runtimePanel(width, bodyHeight))
    }
    lines = append(lines, m.statusView(width), m.footerView(width))
    return fitScreen(strings.Join(lines, "\n"), m.width, m.height)
}
```

Implement `lifecycleSummary` by counting active (`starting`, `running`), problem (`lost`, `error`), inactive (`exited`, `closed`), and unknown statuses. `headerView` renders uppercase connection state plus `<n> panes`, omitting zero-value categories. `runtimePanel` builds all tree lines, takes `m.treeViewport.visible(lines, height-2)`, renders child counts for group nodes and `statusSymbol(status) + " " + pane.ID + " " + role + " [" + status + "]"` for pane nodes, then adds `m.treeViewport.position(len(lines), height-2)` to the panel title. `detailPanel` renders `[Output]  Events` or `Output  [Events]`, visible viewport lines, and its position label. Use a double border for the focused panel and a normal border for the unfocused panel; include `[FOCUSED]` in the focused title so color is not required.

- [ ] **Step 4: Add contextual status and footer rendering**

Use these exact key-hint groups:

```go
func (m Model) footerText() string {
    if m.focus == focusDetail {
        return "j/k scroll  h/l tab  pgup/pgdn page  g/G ends  tab focus  i input  ? help  q quit"
    }
    return "j/k move  enter toggle  tab focus  s snapshot  i input  x cleanup  ? help  q quit"
}
```

`statusView` shows `statusText`, retains distinct error styling when it contains `failed` or the connection is degraded, appends `actionText` when different, and includes `Refreshed just now` when `lastRefresh` is non-zero. Do not display a changing wall-clock seconds counter; it would require a separate UI tick and make tests flaky.

- [ ] **Step 5: Format and verify GREEN, size safety, and legacy assertions**

Run: `gofmt -w internal/dashboard/view.go internal/dashboard/view_test.go && go test ./internal/dashboard -count=1`

Expected: all dashboard tests pass, including `TestViewFitsConfiguredWindow`.

- [ ] **Step 6: Commit responsive rendering**

```bash
git add internal/dashboard/view.go internal/dashboard/view_test.go
git commit -m "feat: improve dashboard information layout"
```

### Task 4: Add Help, Input, and Cleanup Overlays

**Files:**
- Modify: `internal/dashboard/actions.go:18-107`
- Modify: `internal/dashboard/model.go:56-79`
- Modify: `internal/dashboard/view.go`
- Modify: `internal/dashboard/model_test.go`
- Modify: `internal/dashboard/view_test.go`

**Interfaces:**
- Consumes: existing `mode`, `inputPane`, `input`, `confirmTask`, and `taskPaneCount` behavior.
- Produces: mode `help`, `Model.overlayView() string`, and centered bordered overlays composed over the base dashboard.

- [ ] **Step 1: Write failing overlay behavior and rendering tests**

Append to `model_test.go`:

```go
func TestModelHelpModeOnlyClosesOnQuestionMarkOrEscape(t *testing.T) {
    m := NewModel(context.Background(), &fakeClient{}, Options{})
    next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
    m = next.(Model)
    if m.mode != "help" { t.Fatalf("mode = %q", m.mode) }
    next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
    if next.(Model).mode != "help" { t.Fatal("ordinary key closed help") }
    next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
    if next.(Model).mode != "normal" { t.Fatalf("mode = %q", next.(Model).mode) }
}

func TestCleanupOverlayIgnoresInvalidKeys(t *testing.T) {
    _, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
    next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
    next, cmd := next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
    if cmd != nil || next.(Model).mode != "confirm-cleanup" {
        t.Fatalf("invalid cleanup key changed state: %#v", next.(Model))
    }
}
```

Append to `view_test.go`:

```go
func TestViewRendersInputCleanupAndHelpOverlays(t *testing.T) {
    _, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
    m.width, m.height = 100, 24
    cases := []struct{ mode, want string }{
        {"input", "INPUT -> coder"},
        {"confirm-cleanup", "CLEAN UP TASK task-1"},
        {"help", "DASHBOARD HELP"},
    }
    for _, tc := range cases {
        m.mode = tc.mode
        m.inputPane = "coder"
        m.confirmTask = "task-1"
        if view := m.View(); !strings.Contains(view, tc.want) {
            t.Fatalf("mode %s view missing %q: %q", tc.mode, tc.want, view)
        }
    }
}
```

- [ ] **Step 2: Run overlay tests and verify RED**

Run: `go test ./internal/dashboard -run '^(TestModelHelp|TestCleanupOverlay|TestViewRenders.*Overlay)' -count=1`

Expected: help-mode assertions and overlay text assertions fail.

- [ ] **Step 3: Implement help-mode routing**

Extend `updateKey` in `actions.go`:

```go
case "help":
    if msg.Type == tea.KeyEsc || msg.String() == "?" || msg.String() == "q" {
        m.mode = "normal"
        m.statusText = "help closed"
    }
    return m, nil
```

Handle `?` in `updateActionOrNormalKey` before action keys:

```go
case "?":
    if m.actionInFlight { return m, nil }
    m.mode = "help"
    m.statusText = "keyboard help"
    return m, nil
```

Keep input editing and cleanup confirmation routing ahead of normal shortcuts so typed `?`, `q`, `x`, and `r` cannot trigger global actions.

- [ ] **Step 4: Compose bordered overlays in the view**

Implement `overlayView` with exact headings and safety text:

```go
func (m Model) overlayView() string {
    switch m.mode {
    case "input":
        return fmt.Sprintf("INPUT -> %s\n\n> %s\n\nEnter send  Esc cancel", m.inputPane, string(m.input))
    case "confirm-cleanup":
        return fmt.Sprintf("CLEAN UP TASK %s\n\nThis closes %d managed pane(s) in this task.\nOther tasks and unmanaged panes are not touched.\n\ny confirm  n/Esc cancel", m.confirmTask, m.taskPaneCount(m.confirmTask))
    case "help":
        return "DASHBOARD HELP\n\nTab focus panels\nj/k move or scroll\nh/l switch Output and Events\nPgUp/PgDn page  g/G ends\nEnter expand/collapse\ns snapshot  i input\nr reconcile  x cleanup  R refresh\n\n? / q / Esc close"
    default:
        return ""
    }
}
```

Render the overlay after the base view is composed. Use `lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayStyle.Render(content), lipgloss.WithWhitespaceChars(" "))` when width and height are positive. For tiny dimensions, return `fitScreen(overlayStyle.Render(content), m.width, m.height)` so the overlay never causes overflow. The overlay border uses the same error color for cleanup and the focused color for input/help.

- [ ] **Step 5: Format and verify GREEN plus action regressions**

Run: `gofmt -w internal/dashboard/actions.go internal/dashboard/model_test.go internal/dashboard/view.go internal/dashboard/view_test.go && go test ./internal/dashboard -count=1`

Expected: all dashboard tests pass, including existing input and cleanup scoping tests.

- [ ] **Step 6: Commit overlays**

```bash
git add internal/dashboard/actions.go internal/dashboard/model_test.go internal/dashboard/view.go internal/dashboard/view_test.go
git commit -m "feat: add dashboard action overlays"
```

### Task 5: Document and Verify the Complete Dashboard Workflow

**Files:**
- Modify: `docs/manual-smoke-test.md:76-107`
- Verify: `internal/dashboard`, `internal/cli/dashboard`, `cmd/zellij-agent`, and full module.

**Interfaces:**
- Consumes: Tasks 1-4 completed dashboard behavior.
- Produces: a reproducible real-Zellij usability smoke flow and rebuilt custom CLI binary.

- [ ] **Step 1: Extend the manual dashboard smoke checklist**

Replace the dashboard interaction list with these exact checks:

```markdown
1. Confirm the header reports `LIVE`, the managed pane count, and active/problem
   lifecycle totals without relying on color.
2. Use `j`/`k` in the focused Runtime panel, then press `Tab` and verify focus
   moves to Detail without changing the selected pane.
3. In Detail, use `h`/`l` to switch Output and Events. Use `j`/`k`,
   `PageUp`/`PageDown`, and `g`/`G` to verify independent scrolling.
4. Resize below 90 columns and verify only the focused panel is visible; press
   `Tab` to switch the visible panel. Resize back and verify the selection, tab,
   and scroll position remain stable.
5. Press `?` and verify the help overlay. Close it with `Esc`.
6. Select `coder`, press `i`, verify the overlay names `coder`, enter
   `echo dashboard-smoke-ok`, and press Enter. Refresh the snapshot and confirm
   the output contains `dashboard-smoke-ok`.
7. Press `r` and verify the footer reports reconciled active/lost counts.
8. Press `x`, verify the cleanup overlay names task `dashboard-smoke`, shows its
   managed pane count and task-only scope, then press `y`. Confirm only that
   task's managed panes are cleaned up.
9. If the event stream closes, confirm the header reports `DEGRADED` while
   periodic polling, manual refresh, and runtime actions remain available.
```

- [ ] **Step 2: Run focused dashboard and CLI tests**

Run: `go test ./internal/dashboard ./internal/cli/dashboard ./cmd/zellij-agent -count=1`

Expected: all three packages report `ok`.

- [ ] **Step 3: Run the full regression suite**

Run: `go test ./... -count=1`

Expected: every package passes; packages without tests report `[no test files]`.

- [ ] **Step 4: Build and immediately register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Expected: both commands exit with status 0 and `cmp -s bin/zellij-agent ~/.config/custom-cli` succeeds.

- [ ] **Step 5: Inspect the final diff and boundary invariants**

Run:

```bash
gofmt -w internal/dashboard/*.go
git diff --check
if rg -n 'internal/zellij|exec\.Command.*zellij' internal/dashboard; then exit 1; fi
git status --short
```

Expected: `git diff --check` and the boundary scan exit 0; status lists only the intended dashboard and smoke-document changes.

- [ ] **Step 6: Commit documentation and final formatting**

```bash
git add internal/dashboard docs/manual-smoke-test.md
git commit -m "docs: expand dashboard usability smoke flow"
```
