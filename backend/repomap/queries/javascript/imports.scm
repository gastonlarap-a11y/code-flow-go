; What a JavaScript or JSX file imports: static imports, re-exports, require() and import().

(import_statement source: (string (string_fragment) @import))
(export_statement source: (string (string_fragment) @import))
((call_expression
  function: (identifier) @_require
  arguments: (arguments . (string (string_fragment) @import)))
 (#eq? @_require "require"))
(call_expression
  function: (import)
  arguments: (arguments . (string (string_fragment) @import)))
