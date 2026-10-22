package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// Timeout statement_timeout terjadi sebagai SQLSTATE 57014; dipetakan ke
// pesan yang rapi agar explorer tidak menampilkan error mentah PG.
func TestRowsErrorMapsStatementTimeout(t *testing.T) {
	err := rowsError(&pgconn.PgError{
		Code:    "57014",
		Message: "canceling statement due to statement timeout",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "batas waktu")
	require.NotContains(t, err.Error(), "canceling statement due to statement timeout",
		"pesan mentah PG tidak ikut naik ke permukaan")

	generic := rowsError(errors.New("koneksi terputus"))
	require.Error(t, generic)
	require.Contains(t, generic.Error(), "koneksi terputus")
}
