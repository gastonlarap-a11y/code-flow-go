; Declarations in Python. A function inside a class is read as a method by its nesting; a
; module-level assignment is a constant only when its name is upper case, which the code checks.

(class_definition name: (identifier) @name) @class
(function_definition name: (identifier) @name) @function
(module (expression_statement (assignment left: (identifier) @name) @const))
