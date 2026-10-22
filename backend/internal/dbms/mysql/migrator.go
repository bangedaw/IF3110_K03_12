package mysql

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "time"

    "example.com/dbmp/backend/internal/dbms"
    "example.com/dbmp/backend/internal/migration/sqlsplit"
    _ "github.com/go-sql-driver/mysql"
)

type Migrator struct { DB *sql.DB }
func (m Migrator) SupportsTransactionalDDL() bool { return false }
type StatementError struct { Index int; LastSuccessful int; Err error }
func (e *StatementError) Error() string { return fmt.Sprintf("MySQL statement %d failed (last successful statement: %d): %v",e.Index,e.LastSuccessful,e.Err) }
func (e *StatementError) Unwrap() error { return e.Err }

func (m Migrator) Apply(ctx context.Context,in dbms.ApplyInput)(*dbms.ApplyResult,error){
    if m.DB==nil{return nil,errors.New("mysql migrator: nil DB")}
    stmts,err:=sqlsplit.Split(in.SQL,sqlsplit.MySQL);if err!=nil{return nil,err}
    if err=sqlsplit.Validate(stmts,sqlsplit.MySQL);err!=nil{return nil,err}
    if len(stmts)==0{return nil,errors.New("migration contains no SQL statements")}
    last:=0
    for _,st:=range stmts{
        if _,err=m.DB.ExecContext(ctx,st.SQL);err!=nil{return nil,&StatementError{Index:st.Index,LastSuccessful:last,Err:err}}
        last=st.Index
    }
    _,err=m.DB.ExecContext(ctx,`CREATE TABLE IF NOT EXISTS _dbmp_schema_history (
      run_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL, version INT NOT NULL,
      checksum VARBINARY(32) NOT NULL, applied_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
    )`)
    if err!=nil{return nil,fmt.Errorf("create target migration history (schema changes may already exist): %w",err)}
    _,err=m.DB.ExecContext(ctx,`INSERT INTO _dbmp_schema_history(run_id,migration_id,version,checksum) VALUES(?,?,?,?)`,
      in.RunID,in.MigrationID,in.Version,in.Checksum)
    if err!=nil{return nil,fmt.Errorf("write target migration history (schema changes may already exist): %w",err)}
    return &dbms.ApplyResult{StatementsExecuted:len(stmts),AppliedAt:time.Now().UTC()},nil
}
