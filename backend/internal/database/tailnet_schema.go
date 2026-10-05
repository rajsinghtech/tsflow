package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// tailnetTable is one table that stores rows for a single tailnet.
// Fresh databases are created from this list. Existing databases are rebuilt
// into the same shape, with every current row copied to DefaultTailnetID.
type tailnetTable struct {
	name    string
	columns string
	// copySelect reads the pre-tailnet table. The first selected value is the
	// bound tailnet id. Column order matches columns above, excluding the
	// PRIMARY KEY clause.
	copySelect string
	// checksumExpr is compared before the old table is dropped. Empty skips
	// the numeric check and only compares row counts.
	checksumExpr string
	indexes      []string
}

// Indexes lead with tailnet_id so a query for one tailnet is a prefix scan.
// Equality on a single tailnet id, then the same bucket or node column the
// previous schema used, keeps the one-tailnet access path. The endpoint index
// serves the per-pair merges in the graph query when one tailnet has on the
// order of 20k nodes and many minute buckets.
var tailnetTables = []tailnetTable{
	{
		name: "node_pairs",
		columns: `
		tailnet_id TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		src_node_id TEXT NOT NULL,
		dst_node_id TEXT NOT NULL,
		traffic_type TEXT NOT NULL,
		tx_bytes INTEGER DEFAULT 0,
		rx_bytes INTEGER DEFAULT 0,
		tx_pkts INTEGER DEFAULT 0,
		rx_pkts INTEGER DEFAULT 0,
		flow_count INTEGER DEFAULT 0,
		protocols TEXT DEFAULT '[]',
		protocol_bytes TEXT DEFAULT '{}',
		ports TEXT DEFAULT '[]',
		tx_ports TEXT DEFAULT '[]',
		rx_ports TEXT DEFAULT '[]',
		tx_protocol_bytes TEXT DEFAULT '{}',
		rx_protocol_bytes TEXT DEFAULT '{}',
		directional_ports INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tailnet_id, bucket, src_node_id, dst_node_id, traffic_type)`,
		copySelect: `SELECT ?, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports,
			tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
			directional_ports FROM node_pairs`,
		checksumExpr: "tx_bytes + rx_bytes",
		indexes: []string{
			`CREATE INDEX IF NOT EXISTS idx_node_pairs_src ON node_pairs(tailnet_id, src_node_id, bucket)`,
			`CREATE INDEX IF NOT EXISTS idx_node_pairs_dst ON node_pairs(tailnet_id, dst_node_id, bucket)`,
			`CREATE INDEX IF NOT EXISTS idx_node_pairs_endpoints ON node_pairs(tailnet_id, src_node_id, dst_node_id, traffic_type, bucket)`,
		},
	},
	{
		name: "bandwidth",
		columns: `
		tailnet_id TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		tx_bytes INTEGER DEFAULT 0,
		rx_bytes INTEGER DEFAULT 0,
		PRIMARY KEY (tailnet_id, bucket)`,
		copySelect:   `SELECT ?, bucket, tx_bytes, rx_bytes FROM bandwidth`,
		checksumExpr: "tx_bytes + rx_bytes",
	},
	{
		name: "bandwidth_by_node",
		columns: `
		tailnet_id TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		node_id TEXT NOT NULL,
		tx_bytes INTEGER DEFAULT 0,
		rx_bytes INTEGER DEFAULT 0,
		PRIMARY KEY (tailnet_id, bucket, node_id)`,
		copySelect:   `SELECT ?, bucket, node_id, tx_bytes, rx_bytes FROM bandwidth_by_node`,
		checksumExpr: "tx_bytes + rx_bytes",
		indexes: []string{
			`CREATE INDEX IF NOT EXISTS idx_bandwidth_by_node ON bandwidth_by_node(tailnet_id, node_id, bucket)`,
		},
	},
	{
		name: "traffic_stats",
		columns: `
		tailnet_id TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		tcp_bytes INTEGER DEFAULT 0,
		udp_bytes INTEGER DEFAULT 0,
		other_proto_bytes INTEGER DEFAULT 0,
		virtual_bytes INTEGER DEFAULT 0,
		exit_bytes INTEGER DEFAULT 0,
		subnet_bytes INTEGER DEFAULT 0,
		physical_bytes INTEGER DEFAULT 0,
		total_flows INTEGER DEFAULT 0,
		unique_pairs INTEGER DEFAULT 0,
		top_ports TEXT DEFAULT '[]',
		PRIMARY KEY (tailnet_id, bucket)`,
		copySelect: `SELECT ?, bucket, tcp_bytes, udp_bytes, other_proto_bytes,
			virtual_bytes, exit_bytes, subnet_bytes, physical_bytes,
			total_flows, unique_pairs, top_ports FROM traffic_stats`,
		checksumExpr: "tcp_bytes + udp_bytes + total_flows",
	},
	{
		name: "poll_state",
		columns: `
		tailnet_id TEXT NOT NULL PRIMARY KEY,
		last_poll_end DATETIME,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`,
		copySelect: `SELECT ?, last_poll_end, updated_at FROM poll_state`,
	},
	{
		name: "ingested_objects",
		columns: `
		tailnet_id TEXT NOT NULL,
		object_key TEXT NOT NULL,
		last_modified DATETIME,
		size_bytes INTEGER DEFAULT 0,
		flow_count INTEGER DEFAULT 0,
		ingested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		metadata_hydrated INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tailnet_id, object_key)`,
		copySelect: `SELECT ?, object_key, last_modified, size_bytes, flow_count,
			ingested_at, metadata_hydrated FROM ingested_objects`,
		checksumExpr: "flow_count",
		indexes: []string{
			`CREATE INDEX IF NOT EXISTS idx_ingested_objects_ingested_at ON ingested_objects(tailnet_id, ingested_at, object_key)`,
		},
	},
	{
		name: "node_metadata",
		columns: `
		tailnet_id TEXT NOT NULL,
		node_id TEXT NOT NULL,
		name TEXT DEFAULT '',
		hostname TEXT DEFAULT '',
		owner TEXT DEFAULT '',
		ips TEXT DEFAULT '[]',
		tags TEXT DEFAULT '[]',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (tailnet_id, node_id)`,
		copySelect: `SELECT ?, node_id, name, hostname, owner, ips, tags, updated_at
			FROM node_metadata`,
	},
	{
		name: "object_metadata_nodes",
		columns: `
		tailnet_id TEXT NOT NULL,
		object_key TEXT NOT NULL,
		node_id TEXT NOT NULL,
		PRIMARY KEY (tailnet_id, object_key, node_id)`,
		copySelect: `SELECT ?, object_key, node_id FROM object_metadata_nodes`,
		indexes: []string{
			`CREATE INDEX IF NOT EXISTS idx_object_metadata_nodes_node ON object_metadata_nodes(tailnet_id, node_id)`,
		},
	},
}

func (t tailnetTable) createStatement(name string) string {
	// WITHOUT ROWID clusters each table on its primary key, which starts with
	// tailnet_id. A time-range scan for one tailnet then reads that tailnet's
	// rows together instead of hopping through rows from every tailnet.
	return "CREATE TABLE " + name + " (" + t.columns + "\n) WITHOUT ROWID"
}

func (s *SQLiteStore) ensureTailnetSchema(ctx context.Context) error {
	for _, table := range tailnetTables {
		stmt := "CREATE TABLE IF NOT EXISTS " + table.name + " (" + table.columns + "\n) WITHOUT ROWID"
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create %s: %w", table.name, err)
		}
		for _, index := range table.indexes {
			if _, err := s.db.ExecContext(ctx, index); err != nil {
				return fmt.Errorf("failed to create index for %s: %w", table.name, err)
			}
		}
	}
	return nil
}

// migrateTailnetSchema rebuilds tables that still use the single-tailnet
// primary keys. The copy, drop, and rename run in one transaction, so a crash
// or a failed statement leaves the previous tables in place. The rewritten
// tables need about as much free disk as the rows being copied because SQLite
// keeps both copies until the transaction commits.
func (s *SQLiteStore) migrateTailnetSchema(ctx context.Context) error {
	needed, err := s.tailnetMigrationNeeded(ctx)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	// The previous release cannot write the migrated tables. Copy the database
	// before changing it so a rollback can restore that file.
	if err := s.backupBeforeTailnetMigration(ctx); err != nil {
		return err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to reserve a connection for tailnet migration: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("failed to begin tailnet migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var migratedRows int64
	for _, table := range tailnetTables {
		rows, err := s.migrateTailnetTable(ctx, conn, table)
		if err != nil {
			return err
		}
		migratedRows += rows
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("failed to commit tailnet migration: %w", err)
	}
	committed = true
	log.Printf("Updated existing tables to store tailnet id %s (%d rows)", DefaultTailnetID, migratedRows)
	return nil
}

func (s *SQLiteStore) tailnetMigrationNeeded(ctx context.Context) (bool, error) {
	for _, table := range tailnetTables {
		exists, err := s.tableExists(ctx, table.name)
		if err != nil {
			return false, fmt.Errorf("failed to inspect %s before tailnet migration: %w", table.name, err)
		}
		if !exists {
			continue
		}
		hasTailnet, err := s.columnExists(ctx, table.name, "tailnet_id")
		if err != nil {
			return false, fmt.Errorf("failed to inspect %s.tailnet_id: %w", table.name, err)
		}
		if !hasTailnet {
			return true, nil
		}
	}
	return false, nil
}

func (s *SQLiteStore) migrateTailnetTable(ctx context.Context, conn *sql.Conn, table tailnetTable) (int64, error) {
	exists, err := tableExistsQuery(ctx, conn, table.name)
	if err != nil {
		return 0, fmt.Errorf("failed to inspect %s during tailnet migration: %w", table.name, err)
	}
	if !exists {
		return 0, nil
	}
	hasTailnet, err := columnExistsQuery(ctx, conn, table.name, "tailnet_id")
	if err != nil {
		return 0, fmt.Errorf("failed to inspect %s.tailnet_id during tailnet migration: %w", table.name, err)
	}
	if hasTailnet {
		return 0, nil
	}

	tempName := table.name + "__tailnet_new"
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+tempName); err != nil {
		return 0, fmt.Errorf("failed to clear leftover %s: %w", tempName, err)
	}
	if _, err := conn.ExecContext(ctx, table.createStatement(tempName)); err != nil {
		return 0, fmt.Errorf("failed to create %s: %w", tempName, err)
	}
	insert := "INSERT INTO " + tempName + " " + table.copySelect
	if _, err := conn.ExecContext(ctx, insert, DefaultTailnetID); err != nil {
		return 0, fmt.Errorf("failed to copy %s into tailnet %s: %w", table.name, DefaultTailnetID, err)
	}
	oldCount, newCount, err := compareTableCounts(ctx, conn, table.name, tempName)
	if err != nil {
		return 0, fmt.Errorf("failed to verify %s copy: %w", table.name, err)
	}
	if oldCount != newCount {
		return 0, fmt.Errorf("tailnet migration count mismatch for %s: old %d new %d", table.name, oldCount, newCount)
	}
	if table.checksumExpr != "" {
		oldSum, newSum, err := compareChecksum(ctx, conn, table.checksumExpr, table.name, tempName)
		if err != nil {
			return 0, fmt.Errorf("failed to checksum %s: %w", table.name, err)
		}
		if oldSum != newSum {
			return 0, fmt.Errorf("tailnet migration checksum mismatch for %s: old %d new %d", table.name, oldSum, newSum)
		}
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE "+table.name); err != nil {
		return 0, fmt.Errorf("failed to drop pre-tailnet %s: %w", table.name, err)
	}
	// Tests abort here to prove a failed migration rolls the drop back.
	if s.migrateFailAfter == table.name {
		return 0, fmt.Errorf("tailnet migration aborted after dropping %s", table.name)
	}
	if _, err := conn.ExecContext(ctx, "ALTER TABLE "+tempName+" RENAME TO "+table.name); err != nil {
		return 0, fmt.Errorf("failed to rename %s: %w", tempName, err)
	}
	for _, index := range table.indexes {
		if _, err := conn.ExecContext(ctx, index); err != nil {
			return 0, fmt.Errorf("failed to create tailnet index for %s: %w", table.name, err)
		}
	}
	return newCount, nil
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func tableExistsQuery(ctx context.Context, q queryRower, table string) (bool, error) {
	var exists int
	err := q.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", table,
	).Scan(&exists)
	return exists != 0, err
}

func columnExistsQuery(ctx context.Context, q queryRower, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func compareTableCounts(ctx context.Context, q queryRower, oldName, newName string) (int64, int64, error) {
	oldCount, err := scalarCount(ctx, q, "SELECT COUNT(*) FROM "+oldName)
	if err != nil {
		return 0, 0, err
	}
	newCount, err := scalarCount(ctx, q, "SELECT COUNT(*) FROM "+newName)
	if err != nil {
		return 0, 0, err
	}
	return oldCount, newCount, nil
}

func compareChecksum(ctx context.Context, q queryRower, expr, oldName, newName string) (int64, int64, error) {
	oldSum, err := scalarCount(ctx, q, fmt.Sprintf("SELECT COALESCE(SUM(%s), 0) FROM %s", expr, oldName))
	if err != nil {
		return 0, 0, err
	}
	newSum, err := scalarCount(ctx, q, fmt.Sprintf("SELECT COALESCE(SUM(%s), 0) FROM %s", expr, newName))
	if err != nil {
		return 0, 0, err
	}
	return oldSum, newSum, nil
}

func scalarCount(ctx context.Context, q queryRower, query string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, query).Scan(&n)
	return n, err
}
