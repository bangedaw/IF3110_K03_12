package migration

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqlsplit"
)

const (
	maxTitleLen      = 200
	maxTextLen       = 2000
	maxIdentifierLen = 63
	maxBatchSize     = 1_000_000
)

func checksum(sql string) []byte { h := sha256.Sum256([]byte(sql)); return h[:] }

func validateDefinition(kind string, d apigen.MigrationDefinition) error {
	fields := map[string]string{}
	if t := strings.TrimSpace(d.Title); t == "" || len(t) > maxTitleLen {
		fields["title"] = fmt.Sprintf("wajib diisi, maksimal %d karakter", maxTitleLen)
	}
	if d.Description != nil && len(*d.Description) > maxTextLen {
		fields["description"] = fmt.Sprintf("maksimal %d karakter", maxTextLen)
	}
	if msg := validateSQL(kind, d.UpSql, d.Batch != nil); msg != "" {
		fields["upSql"] = msg
	}
	if d.Irreversible {
		if strings.TrimSpace(d.DownSql) != "" {
			fields["downSql"] = "harus kosong untuk migration irreversible"
		}
		if r := strings.TrimSpace(d.IrreversibleReason); r == "" || len(r) > maxTextLen {
			fields["irreversibleReason"] = fmt.Sprintf("wajib diisi untuk migration irreversible, maksimal %d karakter", maxTextLen)
		}
	} else {
		if msg := validateSQL(kind, d.DownSql, d.Batch != nil); msg != "" {
			fields["downSql"] = msg
		}
		if strings.TrimSpace(d.IrreversibleReason) != "" {
			fields["irreversibleReason"] = "hanya untuk migration irreversible"
		}
	}
	if d.Batch != nil {
		if msg := validateBatch(kind, *d.Batch); msg != "" {
			fields["batch"] = msg
		}
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	return nil
}

func validateSQL(kind, sql string, batch bool) string {
	if strings.TrimSpace(sql) == "" {
		return "wajib diisi"
	}
	stmts, err := sqlsplit.Split(sql, sqlsplit.Dialect(kind))
	if err == nil {
		err = sqlsplit.Validate(stmts, sqlsplit.Dialect(kind))
	}
	switch {
	case err != nil:
		return err.Error()
	case len(stmts) == 0:
		return "wajib diisi"
	case batch && (len(stmts) != 1 || !strings.Contains(stmts[0].SQL, "$1") ||
		!strings.Contains(stmts[0].SQL, "$2") || strings.Contains(stmts[0].SQL, "$3")):
		return dbms.ErrBatchSQL.Error()
	}
	return ""
}

func validateBatch(kind string, b apigen.MigrationBatch) string {
	if kind != dbms.KindPostgres {
		return "mode batch hanya untuk PostgreSQL"
	}
	for _, name := range []string{b.Schema, b.Table, b.Column} {
		if strings.TrimSpace(name) == "" || len(name) > maxIdentifierLen {
			return fmt.Sprintf("schema, tabel, dan kolom wajib diisi, maksimal %d karakter", maxIdentifierLen)
		}
	}
	if b.Size < 1 || b.Size > maxBatchSize {
		return fmt.Sprintf("ukuran batch harus 1 sampai %d", maxBatchSize)
	}
	return ""
}
