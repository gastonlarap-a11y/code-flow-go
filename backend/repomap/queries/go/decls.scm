; Declarations in Go. The capture on the whole node names the kind; @name is its name. When two
; patterns match one node, the first one listed wins — so a struct is a struct before it is a type.

(type_spec name: (type_identifier) @name type: (struct_type)) @struct
(type_spec name: (type_identifier) @name type: (interface_type)) @interface
(type_spec name: (type_identifier) @name) @type
(type_alias name: (type_identifier) @name) @type

(function_declaration name: (identifier) @name) @function
(method_declaration name: (field_identifier) @name) @method
(method_elem name: (field_identifier) @name) @method

(source_file (const_declaration (const_spec name: (identifier) @name) @const))
(source_file (var_declaration (var_spec name: (identifier) @name) @var))
