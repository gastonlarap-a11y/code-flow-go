; Declarations in Dart. The capture on the whole node names the kind; @name is its name. A Dart
; function's body is the node after its signature, not inside it, which the code accounts for.

(class_definition name: (identifier) @name) @class
(mixin_declaration (identifier) @name) @mixin
(enum_declaration name: (identifier) @name) @enum
(extension_declaration name: (identifier) @name) @type
(type_alias (type_identifier) @name) @type

(function_signature name: (identifier) @name) @function
(getter_signature name: (identifier) @name) @method
(setter_signature name: (identifier) @name) @method
(constructor_signature name: (identifier) @name) @method
