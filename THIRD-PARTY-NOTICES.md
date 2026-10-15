# Third-party notices

The code map's drawn view (`backend/repomap`, `docs/business-rules/19-repo-map.md`) links
tree-sitter and six of its grammars into the CodeFlow binary through cgo. They are distributed under
the MIT licence, whose terms ask for the copyright notice and the permission notice below to travel
with every copy.

This file covers those components only. The application's other dependencies are listed in
`go.mod` and `frontend/pnpm-lock.yaml`, each with its own licence; gathering every one of them here
is not done yet.

The queries that read each grammar's trees (`backend/repomap/queries/`) are this repository's own.

## Components

| Component | Version | Copyright |
|---|---|---|
| [go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter), with the tree-sitter C runtime it vendors | v0.25.0 | Copyright (c) 2024 Amaan Qureshi; the runtime, Copyright (c) 2018 Max Brunsfeld |
| [tree-sitter-typescript](https://github.com/tree-sitter/tree-sitter-typescript) (TypeScript and TSX) | v0.23.2 | Copyright (c) 2017 Max Brunsfeld |
| [tree-sitter-javascript](https://github.com/tree-sitter/tree-sitter-javascript) | v0.25.0 | Copyright (c) 2014 Max Brunsfeld |
| [tree-sitter-go](https://github.com/tree-sitter/tree-sitter-go) | v0.25.0 | Copyright (c) 2014 Max Brunsfeld |
| [tree-sitter-python](https://github.com/tree-sitter/tree-sitter-python) | v0.25.0 | Copyright (c) 2016 Max Brunsfeld |
| [tree-sitter-java](https://github.com/tree-sitter/tree-sitter-java) | v0.23.5 | Copyright (c) 2017 Ayman Nadeem |
| [tree-sitter-dart](https://github.com/UserNobody14/tree-sitter-dart) | commit `be07cf7118d3` | Copyright (c) 2020-2023 UserNobody14 and others |
| [go-pointer](https://github.com/mattn/go-pointer), used by go-tree-sitter | v0.0.1 | Copyright (c) 2019 Yasuhiro Matsumoto |

## The MIT licence

Each component above is granted under these terms, with its own copyright line:

> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
> associated documentation files (the "Software"), to deal in the Software without restriction,
> including without limitation the rights to use, copy, modify, merge, publish, distribute,
> sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all copies or
> substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT
> NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
> NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
> DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT
> OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
