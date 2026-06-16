# TypeScript LSP call tree

Run the analyzer with a TypeScript or TSX file:

```bash
go run . Page.tsx
```

Expand relative imported components:

```bash
go run . --max-depth 2 Page.tsx
```

Output JSON:

```bash
go run . --format json --max-depth 2 Page.tsx
```

Short form:

```bash
go run . --json --max-depth 2 Page.tsx
```

For raw LSP traffic:

```bash
go run . --trace-lsp Page.tsx
```

The tool starts `typescript-language-server --stdio`, opens the file through LSP,
requests `textDocument/documentSymbol`, and builds a call/component tree from:

- function calls such as `loadProducts()`
- JSX component usage such as `<Header />`
- JSX props at each usage site, such as `onClick={handleClick}`
- relative imports when `--max-depth` is greater than `1`

If `typescript-language-server` is not installed locally or in `PATH`, the tool
uses `npx` with pinned packages.

Example output:

```text
Call/component tree: /path/to/Page.tsx
Page
|- loadProducts
|- Header
|  |- Logo
|  `- UserMenu
`- ProductList
   `- ProductCard
      `- formatProductName
```

Example JSON shape:

```json
{
  "file": "/path/to/Page.tsx",
  "roots": [
    {
      "name": "Page",
      "kind": "component",
      "file": "/path/to/Page.tsx",
      "children": [
        {
          "name": "Header",
          "kind": "component",
          "props": [
            { "name": "title", "value": "Dashboard" }
          ]
        }
      ]
    }
  ]
}
```

Current scope:

- It links declarations inside the same file by default.
- It follows relative imports such as `../view/FooView` when `--max-depth` is
  greater than `1`.
- It does not resolve package imports or project aliases yet.
- It uses lightweight source scanning for calls/JSX after LSP provides symbol
  ranges.
