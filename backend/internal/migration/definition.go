package migration

import (
	"fmt"
	"strings"
)

// Batas ukuran batch. Catatan: README Edo memakai batch sampai 200.000;
// naikkan MaxBatchSize bila itu ukuran yang boleh dipakai pengguna.
const (
	MaxBatchSize = 10000
	MinBatchSize = 1
)

// ValidateDefinition memeriksa up/down/irreversible/batch sebelum disimpan.
// Title dan dialek SQL divalidasi di Service.validateDefinition.
func ValidateDefinition(d Definition) error {
	if strings.TrimSpace(d.UpSQL) == "" {
		return fmt.Errorf("%w: UpSQL wajib diisi", ErrInvalidDefinition)
	}

	if d.Irreversible {
		if strings.TrimSpace(d.DownSQL) != "" {
			return fmt.Errorf("%w: DownSQL harus kosong ketika Irreversible=true", ErrInvalidDefinition)
		}
		if strings.TrimSpace(d.IrreversibleReason) == "" {
			return fmt.Errorf("%w: IrreversibleReason wajib diisi ketika Irreversible=true", ErrInvalidDefinition)
		}
	} else {
		if strings.TrimSpace(d.DownSQL) == "" {
			return fmt.Errorf("%w: DownSQL wajib diisi ketika Irreversible=false", ErrInvalidDefinition)
		}
		if strings.TrimSpace(d.IrreversibleReason) != "" {
			return fmt.Errorf("%w: IrreversibleReason harus kosong ketika Irreversible=false", ErrInvalidDefinition)
		}
	}

	if d.Batch != nil {
		if err := ValidateBatchConfig(*d.Batch); err != nil {
			return err
		}
		if err := ValidateBatchSQL(d.UpSQL, d.DownSQL); err != nil {
			return err
		}
	}
	return nil
}

// ValidateBatchSQL memastikan up dan down masing-masing satu statement yang
// memakai $1 (cursor terakhir, eksklusif) dan $2 (batas batch, inklusif).
func ValidateBatchSQL(up, down string) error {
	if err := validateBatchStatement(up, "UpSQL"); err != nil {
		return err
	}
	return validateBatchStatement(down, "DownSQL")
}

func validateBatchStatement(sql, field string) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("%w: %s kosong", ErrInvalidBatchSQL, field)
	}
	stmts, params, err := scanSQL(sql)
	if err != nil {
		return fmt.Errorf("%w: %s gagal dibaca: %v", ErrInvalidBatchSQL, field, err)
	}
	if len(stmts) != 1 {
		return fmt.Errorf("%w: %s harus berisi tepat satu statement, ditemukan %d", ErrInvalidBatchSQL, field, len(stmts))
	}
	seen := map[int]bool{}
	for _, p := range params {
		if p < 1 || p > 2 {
			return fmt.Errorf("%w: %s memakai placeholder $%d; hanya $1 dan $2 diizinkan", ErrInvalidBatchSQL, field, p)
		}
		seen[p] = true
	}
	if !seen[1] || !seen[2] {
		return fmt.Errorf("%w: %s harus memakai $1 (cursor terakhir) dan $2 (batas batch)", ErrInvalidBatchSQL, field)
	}
	return nil
}

// ValidateBatchConfig: kolom wajib, ukuran dalam rentang.
func ValidateBatchConfig(c BatchConfig) error {
	if strings.TrimSpace(c.Column) == "" {
		return fmt.Errorf("%w: Column wajib diisi", ErrInvalidBatchConfig)
	}
	if !isSimpleIdentifier(c.Column) {
		return fmt.Errorf("%w: Column %q bukan identifier yang valid", ErrInvalidBatchConfig, c.Column)
	}
	if c.Size < MinBatchSize {
		return fmt.Errorf("%w: Size minimal %d, didapat %d", ErrInvalidBatchConfig, MinBatchSize, c.Size)
	}
	if c.Size > MaxBatchSize {
		return fmt.Errorf("%w: Size maksimal %d, didapat %d", ErrInvalidBatchConfig, MaxBatchSize, c.Size)
	}
	return nil
}

func isSimpleIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// EnsureSQLEditable menolak perubahan SQL bila migration pernah dieksekusi.
func EnsureSQLEditable(m Migration) error {
	if m.SQLLocked {
		return fmt.Errorf("%w: migration %d sudah pernah dieksekusi (revisi %d)", ErrSQLNotEditable, m.ID, m.Revision)
	}
	return nil
}
