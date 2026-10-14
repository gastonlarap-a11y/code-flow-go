package codemap

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Tables and the SQL that touches them (CODEMAP-006).

// tableSet maps each live table to where it was last created or renamed.
type tableSet map[string]Pos

var (
	createTableRe = regexp.MustCompile("(?i)\\bCREATE\\s+(?:TEMP(?:ORARY)?\\s+|VIRTUAL\\s+)?TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?[\"`\\[]?(?:\\w+[\"`\\]]?\\.)?[\"`\\[]?(\\w+)")
	dropTableRe   = regexp.MustCompile("(?i)\\bDROP\\s+TABLE\\s+(?:IF\\s+EXISTS\\s+)?[\"`\\[]?(?:\\w+[\"`\\]]?\\.)?[\"`\\[]?(\\w+)")
	renameTableRe = regexp.MustCompile("(?i)\\bALTER\\s+TABLE\\s+[\"`\\[]?(?:\\w+[\"`\\]]?\\.)?[\"`\\[]?(\\w+)[\"`\\]]?\\s+RENAME\\s+TO\\s+[\"`\\[]?(\\w+)")
	sqlCommentRe  = regexp.MustCompile(`--[^\n]*`)
)

// replaySchema applies the table DDL of every `.sql` file in the repository, in file-name order,
// and answers the tables that exist at the end: one a later migration drops or renames is not live,
// whatever its CREATE said. Down-migrations are skipped — replaying them would drop everything the
// up-migrations created.
func replaySchema(root string, sqlFiles []string) (tableSet, error) {
	files := slices.Clone(sqlFiles)
	slices.SortFunc(files, func(a, b string) int {
		return cmp.Or(strings.Compare(filepath.Base(a), filepath.Base(b)), strings.Compare(a, b))
	})

	tables := tableSet{}
	for _, path := range files {
		if strings.HasSuffix(strings.ToLower(path), ".down.sql") {
			continue
		}
		source, err := os.ReadFile(path) //nolint:gosec // G304: a file found by walking the repository the user opened
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		rel, err := relPath(root, path)
		if err != nil {
			return nil, err
		}
		applyDDL(tables, rel, string(source))
	}
	return tables, nil
}

type ddlOp struct {
	offset   int
	kind     byte // 'c'reate, 'd'rop, 'r'ename
	name, to string
}

// applyDDL replays one file's CREATE / DROP / RENAME TABLE statements in source order.
func applyDDL(tables tableSet, file, source string) {
	// Comments are blanked rather than removed, so offsets — and therefore line numbers — hold.
	source = sqlCommentRe.ReplaceAllStringFunc(source, func(c string) string { return strings.Repeat(" ", len(c)) })

	ops := make([]ddlOp, 0, 8)
	for _, m := range createTableRe.FindAllStringSubmatchIndex(source, -1) {
		ops = append(ops, ddlOp{offset: m[0], kind: 'c', name: source[m[2]:m[3]]})
	}
	for _, m := range dropTableRe.FindAllStringSubmatchIndex(source, -1) {
		ops = append(ops, ddlOp{offset: m[0], kind: 'd', name: source[m[2]:m[3]]})
	}
	for _, m := range renameTableRe.FindAllStringSubmatchIndex(source, -1) {
		ops = append(ops, ddlOp{offset: m[0], kind: 'r', name: source[m[2]:m[3]], to: source[m[4]:m[5]]})
	}
	slices.SortFunc(ops, func(a, b ddlOp) int { return cmp.Compare(a.offset, b.offset) })

	for _, op := range ops {
		pos := Pos{File: file, Line: 1 + strings.Count(source[:op.offset], "\n")}
		switch op.kind {
		case 'c':
			tables[strings.ToLower(op.name)] = pos
		case 'd':
			delete(tables, strings.ToLower(op.name))
		case 'r':
			delete(tables, strings.ToLower(op.name))
			tables[strings.ToLower(op.to)] = pos
		}
	}
}

// addCreatedTables adds the tables a string in the code creates — an embedded schema, a table the
// code makes for itself — to the replayed ones. Only CREATE counts here: a DROP in code is something
// the program does at run time, not the shape of the schema.
func addCreatedTables(tables tableSet, text string, at Pos) {
	for _, m := range createTableRe.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(m[1])
		if _, known := tables[name]; !known {
			tables[name] = at
		}
	}
}

var (
	// sqlVerbRe finds the keywords that put a table name right after them, in upper case: that is
	// how SQL is written in the overwhelming majority of code, and prose ("read from cards") is not.
	sqlVerbRe = regexp.MustCompile(`\b(DELETE\s+FROM|INSERT(?:\s+OR\s+[A-Z]+)?\s+INTO|REPLACE\s+INTO|UPDATE|FROM|JOIN)\b`)
	// sqlVerbAnyCaseRe is the same in any case, used only for a string that opens like a statement.
	sqlVerbAnyCaseRe = regexp.MustCompile(`(?i)\b(DELETE\s+FROM|INSERT(?:\s+OR\s+[A-Z]+)?\s+INTO|REPLACE\s+INTO|UPDATE|FROM|JOIN)\b`)
	sqlStatementRe   = regexp.MustCompile(`(?i)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH|REPLACE)\b`)
	sqlIdentRe       = regexp.MustCompile("^\\s*[\"`\\[]?(?:\\w+[\"`\\]]?\\.)?[\"`\\[]?([A-Za-z_][A-Za-z0-9_]*)")
)

// tableAccess is one table a SQL fragment reads or writes.
type tableAccess struct {
	table string
	kind  EdgeKind
}

// classifySQL answers the known tables a fragment reads (FROM, JOIN) or writes (INSERT / REPLACE
// INTO, UPDATE, DELETE FROM). dynamic is true when a table slot holds a placeholder or ends the
// fragment: the name is assembled at run time (`"… FROM " + table`, `FROM ${table}`, `FROM %s`).
//
// A fragment that is exactly a table name is a mention: a table handed to a generic helper.
func classifySQL(text string, tables tableSet) (accesses []tableAccess, dynamic bool) {
	if _, known := tables[text]; known {
		return []tableAccess{{table: text, kind: EdgeMention}}, false
	}

	verbs := sqlVerbRe
	if sqlStatementRe.MatchString(text) {
		verbs = sqlVerbAnyCaseRe
	}
	for _, m := range verbs.FindAllStringSubmatchIndex(text, -1) {
		verb := strings.ToUpper(text[m[2]:m[3]])
		rest := text[m[1]:]
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		if trimmed == "" || strings.HasPrefix(trimmed, "?") || strings.HasPrefix(trimmed, "%") ||
			strings.HasPrefix(trimmed, "$") || strings.HasPrefix(trimmed, "{") {
			dynamic = true
			continue
		}
		ident := sqlIdentRe.FindStringSubmatch(rest)
		if ident == nil {
			continue // a subquery: "FROM (SELECT …)"
		}
		name := strings.ToLower(ident[1])
		if _, known := tables[name]; !known {
			continue // "DO UPDATE SET", sqlite_master, a CTE's own name
		}
		kind := EdgeWrite
		if verb == "FROM" || verb == "JOIN" {
			kind = EdgeRead
		}
		accesses = append(accesses, tableAccess{table: name, kind: kind})
	}
	return accesses, dynamic
}

// looksLikeSQL is the cheap filter in front of classifySQL: a string worth classifying names a known
// table somewhere in it.
func looksLikeSQL(text string, tables tableSet) bool {
	if len(text) > 64*1024 || len(tables) == 0 {
		return false
	}
	if _, known := tables[text]; known {
		return true
	}
	return sqlVerbAnyCaseRe.MatchString(text)
}

func tableID(table string) string { return "sql:" + table }
