; Declarations in JavaScript and JSX. The capture on the whole node names the kind; @name is its
; name. When two patterns match one node, the first one listed wins.

(class_declaration name: (_) @name) @class

(function_declaration name: (_) @name) @function
(generator_function_declaration name: (_) @name) @function

(method_definition name: (_) @name) @method
(field_definition
  property: (_) @name
  value: [(arrow_function) (function_expression) (generator_function)]) @method
(pair
  key: (property_identifier) @name
  value: [(arrow_function) (function_expression)]) @method

(variable_declarator
  name: (identifier) @name
  value: [(arrow_function) (function_expression) (generator_function)]) @function
(variable_declarator name: (identifier) @name value: (class)) @class

; A `let` is a variable; listed first, so it wins over the `const` pattern below for the same node.
(program (lexical_declaration kind: "let" (variable_declarator name: (identifier) @name) @var))
(program (export_statement declaration: (lexical_declaration kind: "let" (variable_declarator name: (identifier) @name) @var)))
(program (lexical_declaration (variable_declarator name: (identifier) @name) @const))
(program (export_statement declaration: (lexical_declaration (variable_declarator name: (identifier) @name) @const)))
