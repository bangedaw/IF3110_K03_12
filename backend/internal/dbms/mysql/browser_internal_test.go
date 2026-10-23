package mysql

import (
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestRowsErrorMapsExecutionTimeout(t *testing.T) {
	for _, number := range []uint16{1317, 3024} {
		err := rowsError(&mysql.MySQLError{Number: number, Message: "Query execution was interrupted"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "batas waktu")
		require.NotContains(t, err.Error(), "interrupted", "pesan mentah MySQL tidak ikut naik ke permukaan")
	}

	generic := rowsError(errors.New("koneksi terputus"))
	require.Error(t, generic)
	require.Contains(t, generic.Error(), "koneksi terputus")

	require.NoError(t, rowsError(nil))
}
