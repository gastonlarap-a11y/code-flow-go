package dbml

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// The four catalogues (DBML-026).
//
// Three of them read **member order** from a system catalogue rather than from `information_schema`,
// and that is the one thing they share: a composite key read through `constraint_column_usage` comes
// back with its columns unordered, which pairs the wrong ones together and is silent about it.

// ---- PostgreSQL ----------------------------------------------------------------------------------

type postgresIntrospector struct{}

func (postgresIntrospector) dsn(connection Connection, password string) (string, string, error) {
	target := &url.URL{
		Scheme: "postgres",
		Host:   hostPort(connection, 5432),
		Path:   "/" + stringOf(connection.Database),
		User:   userInfo(connection.Username, password),
	}

	mode := "disable"
	if connection.UseTLS {
		// `require` rather than `verify-full`: the user asked for encryption, and a schema designer
		// that refused a staging server with a self-signed certificate would be refusing the
		// commonest case it exists for.
		mode = "require"
	}
	target.RawQuery = url.Values{"sslmode": {mode}}.Encode()

	return "pgx", target.String(), nil
}

func (p postgresIntrospector) read(ctx context.Context, db *sql.DB, _ Connection, into *snapshotBuilder) error {
	// Columns from `information_schema`, everything else from `pg_catalog` — which is the only
	// place member order survives.
	//
	// **The `COALESCE` is load-bearing.** Without it a column with no default gives
	// `false OR NULL`, which is `NULL`, and every plain column fails to read as a boolean.
	if err := p.readColumns(ctx, db, into); err != nil {
		return err
	}
	if err := p.readConstraints(ctx, db, into); err != nil {
		return err
	}
	if err := p.readRefs(ctx, db, into); err != nil {
		return err
	}
	if err := p.readIndexes(ctx, db, into); err != nil {
		return err
	}
	return p.readEnums(ctx, db, into)
}

func (postgresIntrospector) readColumns(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "columns", `
		SELECT c.table_schema, c.table_name, c.column_name,
		       COALESCE(c.domain_name, format_type(a.atttypid, a.atttypmod)) AS data_type,
		       c.is_nullable = 'NO' AS not_null,
		       (c.is_identity = 'YES' OR COALESCE(c.column_default, '') LIKE 'nextval(%') AS increment,
		       c.column_default, c.ordinal_position
		  FROM information_schema.columns c
		  JOIN pg_catalog.pg_namespace n ON n.nspname = c.table_schema
		  JOIN pg_catalog.pg_class t ON t.relname = c.table_name AND t.relnamespace = n.oid
		  JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attname = c.column_name
		 WHERE c.table_schema NOT IN ('pg_catalog', 'information_schema')
		   AND t.relkind IN ('r', 'p')
		 ORDER BY c.table_schema, c.table_name, c.ordinal_position`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row columnRow
		var defaultValue sql.NullString

		if err := rows.Scan(&row.schema, &row.table, &row.name, &row.dataType,
			&row.notNull, &row.increment, &defaultValue, &row.position); err != nil {
			return fmt.Errorf("scan a column: %w", err)
		}
		// `nextval(…)` is dropped as a default because `increment` already says it, and printing
		// both would put the sequence's name in the diagram.
		row.defaultValue = nonEmpty(stripPostgresDefault(text(defaultValue)))
		into.columns = append(into.columns, row)
	}
	return rows.Err()
}

func (postgresIntrospector) readConstraints(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "keys", `
		SELECT n.nspname, t.relname, c.conname,
		       a.attname,
		       CASE c.contype WHEN 'p' THEN 'p' ELSE 'u' END,
		       k.ordinality
		  FROM pg_catalog.pg_constraint c
		  JOIN pg_catalog.pg_class t ON t.oid = c.conrelid
		  JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
		  JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS k(attnum, ordinality) ON TRUE
		  JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		 WHERE c.contype IN ('p', 'u')
		   AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		 ORDER BY n.nspname, t.relname, c.conname, k.ordinality`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row constraintRow
		if err := rows.Scan(&row.schema, &row.table, &row.constraint, &row.column,
			&row.kind, &row.position); err != nil {
			return fmt.Errorf("scan a key: %w", err)
		}
		into.constraints = append(into.constraints, row)
	}
	return rows.Err()
}

func (postgresIntrospector) readRefs(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "relations", `
		SELECT c.conname,
		       fn.nspname, ft.relname, fa.attname,
		       tn.nspname, tt.relname, ta.attname,
		       c.confdeltype, c.confupdtype, k.ordinality
		  FROM pg_catalog.pg_constraint c
		  JOIN pg_catalog.pg_class ft ON ft.oid = c.conrelid
		  JOIN pg_catalog.pg_namespace fn ON fn.oid = ft.relnamespace
		  JOIN pg_catalog.pg_class tt ON tt.oid = c.confrelid
		  JOIN pg_catalog.pg_namespace tn ON tn.oid = tt.relnamespace
		  JOIN LATERAL unnest(c.conkey, c.confkey) WITH ORDINALITY AS k(from_attnum, to_attnum, ordinality) ON TRUE
		  JOIN pg_catalog.pg_attribute fa ON fa.attrelid = ft.oid AND fa.attnum = k.from_attnum
		  JOIN pg_catalog.pg_attribute ta ON ta.attrelid = tt.oid AND ta.attnum = k.to_attnum
		 WHERE c.contype = 'f'
		   AND fn.nspname NOT IN ('pg_catalog', 'information_schema')
		 ORDER BY fn.nspname, ft.relname, c.conname, k.ordinality`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row refRow
		var onDelete, onUpdate string

		if err := rows.Scan(&row.constraint,
			&row.fromSchema, &row.fromTable, &row.fromColumn,
			&row.toSchema, &row.toTable, &row.toColumn,
			&onDelete, &onUpdate, &row.position); err != nil {
			return fmt.Errorf("scan a relation: %w", err)
		}
		// PostgreSQL reports the action as a single character; `normalizeAction` knows the five.
		row.onDelete, row.onUpdate = &onDelete, &onUpdate
		into.refs = append(into.refs, row)
	}
	return rows.Err()
}

func (postgresIntrospector) readIndexes(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "indexes", `
		SELECT n.nspname, t.relname, i.relname, a.attname,
		       ix.indisunique, ix.indisprimary, k.ordinality
		  FROM pg_catalog.pg_index ix
		  JOIN pg_catalog.pg_class i ON i.oid = ix.indexrelid
		  JOIN pg_catalog.pg_class t ON t.oid = ix.indrelid
		  JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
		  JOIN LATERAL unnest(ix.indkey::int[]) WITH ORDINALITY AS k(attnum, ordinality) ON TRUE
		  JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		 WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		 ORDER BY n.nspname, t.relname, i.relname, k.ordinality`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row indexRow
		if err := rows.Scan(&row.schema, &row.table, &row.name, &row.column,
			&row.unique, &row.primary, &row.position); err != nil {
			return fmt.Errorf("scan an index: %w", err)
		}
		into.indexes = append(into.indexes, row)
	}
	return rows.Err()
}

// readEnums is PostgreSQL's alone: it is the only engine with real enumerated types.
func (postgresIntrospector) readEnums(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "enums", `
		SELECT n.nspname, t.typname, e.enumlabel, e.enumsortorder
		  FROM pg_catalog.pg_type t
		  JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
		  JOIN pg_catalog.pg_enum e ON e.enumtypid = t.oid
		 WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		 ORDER BY n.nspname, t.typname, e.enumsortorder`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row enumRow
		var order float64

		if err := rows.Scan(&row.schema, &row.name, &row.value, &order); err != nil {
			return fmt.Errorf("scan an enum value: %w", err)
		}
		row.position = int(order)
		into.enums = append(into.enums, row)
	}
	return rows.Err()
}

var postgresCast = regexp.MustCompile(`::[\w ."]+$`)

// stripPostgresDefault unwraps the `::cast` PostgreSQL appends, and drops a sequence default.
func stripPostgresDefault(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if strings.HasPrefix(trimmed, "nextval(") {
		return nil
	}

	stripped := strings.TrimSpace(postgresCast.ReplaceAllString(trimmed, ""))
	stripped = strings.Trim(stripped, "'")
	return &stripped
}

// ---- SQL Server ----------------------------------------------------------------------------------

type sqlServerIntrospector struct{}

func (sqlServerIntrospector) dsn(connection Connection, password string) (string, string, error) {
	values := url.Values{"database": {stringOf(connection.Database)}}
	if connection.UseTLS {
		values.Set("encrypt", "true")
		// The same judgement PostgreSQL's `require` makes: encryption without demanding a
		// certificate chain a staging server usually does not have.
		values.Set("trustservercertificate", "true")
	} else {
		values.Set("encrypt", "disable")
	}

	target := &url.URL{
		Scheme:   "sqlserver",
		Host:     hostPort(connection, 1433),
		User:     userInfo(connection.Username, password),
		RawQuery: values.Encode(),
	}
	return "sqlserver", target.String(), nil
}

func (s sqlServerIntrospector) read(ctx context.Context, db *sql.DB, _ Connection, into *snapshotBuilder) error {
	// `INFORMATION_SCHEMA` for columns and `sys` for keys, relations and indexes — the same
	// ordering reason, here `sys.index_columns.key_ordinal`.
	if err := s.readColumns(ctx, db, into); err != nil {
		return err
	}
	if err := s.readConstraints(ctx, db, into); err != nil {
		return err
	}
	if err := s.readRefs(ctx, db, into); err != nil {
		return err
	}
	return s.readIndexes(ctx, db, into)
}

func (sqlServerIntrospector) readColumns(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "columns", `
		SELECT c.TABLE_SCHEMA, c.TABLE_NAME, c.COLUMN_NAME,
		       c.DATA_TYPE
		         + CASE
		             WHEN c.DATA_TYPE IN ('varchar','nvarchar','char','nchar','varbinary','binary')
		               THEN '(' + CASE WHEN c.CHARACTER_MAXIMUM_LENGTH = -1 THEN 'max'
		                               ELSE CAST(c.CHARACTER_MAXIMUM_LENGTH AS varchar(12)) END + ')'
		             WHEN c.DATA_TYPE IN ('decimal','numeric')
		               THEN '(' + CAST(c.NUMERIC_PRECISION AS varchar(12)) + ','
		                        + CAST(c.NUMERIC_SCALE AS varchar(12)) + ')'
		             ELSE ''
		           END AS data_type,
		       CASE WHEN c.IS_NULLABLE = 'NO' THEN 1 ELSE 0 END,
		       COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)),
		                      c.COLUMN_NAME, 'IsIdentity') AS increment,
		       c.COLUMN_DEFAULT, c.ORDINAL_POSITION
		  FROM INFORMATION_SCHEMA.COLUMNS c
		  JOIN INFORMATION_SCHEMA.TABLES t
		    ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
		 WHERE t.TABLE_TYPE = 'BASE TABLE'
		 ORDER BY c.TABLE_SCHEMA, c.TABLE_NAME, c.ORDINAL_POSITION`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row columnRow
		var notNull int
		var increment sql.NullInt64
		var defaultValue sql.NullString

		if err := rows.Scan(&row.schema, &row.table, &row.name, &row.dataType,
			&notNull, &increment, &defaultValue, &row.position); err != nil {
			return fmt.Errorf("scan a column: %w", err)
		}
		row.notNull = notNull == 1
		row.increment = increment.Valid && increment.Int64 == 1
		row.defaultValue = nonEmpty(stripSQLServerDefault(text(defaultValue)))
		into.columns = append(into.columns, row)
	}
	return rows.Err()
}

func (sqlServerIntrospector) readConstraints(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "keys", `
		SELECT SCHEMA_NAME(t.schema_id), t.name, k.name, c.name,
		       CASE WHEN k.is_primary_key = 1 THEN 'p' ELSE 'u' END,
		       ic.key_ordinal
		  FROM sys.key_constraints k
		  JOIN sys.tables t ON t.object_id = k.parent_object_id
		  JOIN sys.index_columns ic
		    ON ic.object_id = k.parent_object_id AND ic.index_id = k.unique_index_id
		  JOIN sys.columns c ON c.object_id = t.object_id AND c.column_id = ic.column_id
		 ORDER BY SCHEMA_NAME(t.schema_id), t.name, k.name, ic.key_ordinal`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row constraintRow
		if err := rows.Scan(&row.schema, &row.table, &row.constraint, &row.column,
			&row.kind, &row.position); err != nil {
			return fmt.Errorf("scan a key: %w", err)
		}
		into.constraints = append(into.constraints, row)
	}
	return rows.Err()
}

func (sqlServerIntrospector) readRefs(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "relations", `
		SELECT fk.name,
		       SCHEMA_NAME(ft.schema_id), ft.name, fc.name,
		       SCHEMA_NAME(tt.schema_id), tt.name, tc.name,
		       fk.delete_referential_action_desc, fk.update_referential_action_desc,
		       fkc.constraint_column_id
		  FROM sys.foreign_keys fk
		  JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
		  JOIN sys.tables ft ON ft.object_id = fk.parent_object_id
		  JOIN sys.columns fc ON fc.object_id = ft.object_id AND fc.column_id = fkc.parent_column_id
		  JOIN sys.tables tt ON tt.object_id = fk.referenced_object_id
		  JOIN sys.columns tc
		    ON tc.object_id = tt.object_id AND tc.column_id = fkc.referenced_column_id
		 ORDER BY SCHEMA_NAME(ft.schema_id), ft.name, fk.name, fkc.constraint_column_id`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row refRow
		var onDelete, onUpdate sql.NullString

		if err := rows.Scan(&row.constraint,
			&row.fromSchema, &row.fromTable, &row.fromColumn,
			&row.toSchema, &row.toTable, &row.toColumn,
			&onDelete, &onUpdate, &row.position); err != nil {
			return fmt.Errorf("scan a relation: %w", err)
		}
		row.onDelete, row.onUpdate = text(onDelete), text(onUpdate)
		into.refs = append(into.refs, row)
	}
	return rows.Err()
}

func (sqlServerIntrospector) readIndexes(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "indexes", `
		SELECT SCHEMA_NAME(t.schema_id), t.name, i.name, c.name,
		       i.is_unique, i.is_primary_key, ic.key_ordinal
		  FROM sys.indexes i
		  JOIN sys.tables t ON t.object_id = i.object_id
		  JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		  JOIN sys.columns c ON c.object_id = t.object_id AND c.column_id = ic.column_id
		 WHERE i.name IS NOT NULL AND ic.is_included_column = 0
		 ORDER BY SCHEMA_NAME(t.schema_id), t.name, i.name, ic.key_ordinal`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row indexRow
		if err := rows.Scan(&row.schema, &row.table, &row.name, &row.column,
			&row.unique, &row.primary, &row.position); err != nil {
			return fmt.Errorf("scan an index: %w", err)
		}
		into.indexes = append(into.indexes, row)
	}
	return rows.Err()
}

// stripSQLServerDefault unwraps the doubled parentheses, and the `N` Unicode prefix with them.
//
// Without the `N`, a string default reaches the diagram as `N'algo'` — part of the value as far as
// the reader is concerned.
func stripSQLServerDefault(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	for strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	trimmed = strings.TrimPrefix(trimmed, "N")
	trimmed = strings.Trim(trimmed, "'")

	return &trimmed
}

// ---- MySQL and MariaDB ----------------------------------------------------------------------------

type mysqlIntrospector struct{}

func (mysqlIntrospector) dsn(connection Connection, password string) (string, string, error) {
	tls := "false"
	if connection.UseTLS {
		// `skip-verify` for the same reason the other two relax verification: the user asked for
		// encryption, not for a certificate authority a staging server rarely has.
		tls = "skip-verify"
	}

	return "mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s?tls=%s&parseTime=false&interpolateParams=false",
		stringOf(connection.Username), password, hostPort(connection, 3306),
		stringOf(connection.Database), tls), nil
}

func (m mysqlIntrospector) read(ctx context.Context, db *sql.DB, _ Connection, into *snapshotBuilder) error {
	// **Every query is scoped with `DATABASE()`**, without which `information_schema` returns every
	// table on the server — a schema diagram of somebody else's application.
	if err := m.readColumns(ctx, db, into); err != nil {
		return err
	}
	if err := m.readConstraints(ctx, db, into); err != nil {
		return err
	}
	if err := m.readRefs(ctx, db, into); err != nil {
		return err
	}
	return m.readIndexes(ctx, db, into)
}

func (mysqlIntrospector) readColumns(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "columns", `
		SELECT c.TABLE_SCHEMA, c.TABLE_NAME, c.COLUMN_NAME, c.COLUMN_TYPE,
		       c.IS_NULLABLE = 'NO', c.EXTRA LIKE '%auto_increment%',
		       c.COLUMN_DEFAULT, c.ORDINAL_POSITION
		  FROM information_schema.COLUMNS c
		  JOIN information_schema.TABLES t
		    ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
		 WHERE c.TABLE_SCHEMA = DATABASE() AND t.TABLE_TYPE = 'BASE TABLE'
		 ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row columnRow
		var defaultValue sql.NullString

		// `COLUMN_TYPE` already carries the arguments and `EXTRA` says `auto_increment` outright,
		// which is what makes this the friendliest of the four.
		if err := rows.Scan(&row.schema, &row.table, &row.name, &row.dataType,
			&row.notNull, &row.increment, &defaultValue, &row.position); err != nil {
			return fmt.Errorf("scan a column: %w", err)
		}
		row.defaultValue = nonEmpty(text(defaultValue))
		into.columns = append(into.columns, row)
	}
	return rows.Err()
}

func (mysqlIntrospector) readConstraints(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "keys", `
		SELECT k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME,
		       CASE WHEN t.CONSTRAINT_TYPE = 'PRIMARY KEY' THEN 'p' ELSE 'u' END,
		       k.ORDINAL_POSITION
		  FROM information_schema.KEY_COLUMN_USAGE k
		  JOIN information_schema.TABLE_CONSTRAINTS t
		    ON t.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
		   AND t.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		   AND t.TABLE_NAME = k.TABLE_NAME
		 WHERE k.TABLE_SCHEMA = DATABASE()
		   AND t.CONSTRAINT_TYPE IN ('PRIMARY KEY', 'UNIQUE')
		 ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row constraintRow
		if err := rows.Scan(&row.schema, &row.table, &row.constraint, &row.column,
			&row.kind, &row.position); err != nil {
			return fmt.Errorf("scan a key: %w", err)
		}
		into.constraints = append(into.constraints, row)
	}
	return rows.Err()
}

func (mysqlIntrospector) readRefs(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "relations", `
		SELECT k.CONSTRAINT_NAME,
		       k.TABLE_SCHEMA, k.TABLE_NAME, k.COLUMN_NAME,
		       k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
		       r.DELETE_RULE, r.UPDATE_RULE, k.ORDINAL_POSITION
		  FROM information_schema.KEY_COLUMN_USAGE k
		  JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		    ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
		   AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		   AND r.TABLE_NAME = k.TABLE_NAME
		 WHERE k.TABLE_SCHEMA = DATABASE() AND k.REFERENCED_TABLE_NAME IS NOT NULL
		 ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row refRow
		var onDelete, onUpdate sql.NullString

		if err := rows.Scan(&row.constraint,
			&row.fromSchema, &row.fromTable, &row.fromColumn,
			&row.toSchema, &row.toTable, &row.toColumn,
			&onDelete, &onUpdate, &row.position); err != nil {
			return fmt.Errorf("scan a relation: %w", err)
		}
		row.onDelete, row.onUpdate = text(onDelete), text(onUpdate)
		into.refs = append(into.refs, row)
	}
	return rows.Err()
}

func (mysqlIntrospector) readIndexes(ctx context.Context, db *sql.DB, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "indexes", `
		SELECT TABLE_SCHEMA, TABLE_NAME, INDEX_NAME, COLUMN_NAME,
		       NON_UNIQUE = 0, INDEX_NAME = 'PRIMARY', SEQ_IN_INDEX
		  FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA = DATABASE()
		 ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var row indexRow
		var column sql.NullString

		if err := rows.Scan(&row.schema, &row.table, &row.name, &column,
			&row.unique, &row.primary, &row.position); err != nil {
			return fmt.Errorf("scan an index: %w", err)
		}
		if !column.Valid {
			// A functional index has no column name; there is nothing to draw it against.
			continue
		}
		row.column = column.String
		into.indexes = append(into.indexes, row)
	}
	return rows.Err()
}
