package mysql

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    "time"

    "example.com/dbmp/backend/internal/dbms"
    "example.com/dbmp/backend/internal/migration/sqlsplit"
)

type Explainer struct { DB *sql.DB }
func (e Explainer) Explain(ctx context.Context,in dbms.ExplainInput)(*dbms.ExplainResult,error){
    if e.DB==nil{return nil,errors.New("mysql explainer: nil DB")}
    stmts,err:=sqlsplit.Split(in.SQL,sqlsplit.MySQL);if err!=nil{return nil,err}
    if err=sqlsplit.Validate(stmts,sqlsplit.MySQL);err!=nil{return nil,err}
    hasDDL:=false
    for _,s:=range stmts{if !mysqlExplainable(first(s.SQL)){hasDDL=true}}
    if in.Analyze&&hasDDL{return nil,errors.New("MySQL EXPLAIN ANALYZE is disabled for scripts containing DDL")}
    tx,err:=e.DB.BeginTx(ctx,&sql.TxOptions{ReadOnly:false});if err!=nil{return nil,err}
    defer tx.Rollback() // always rollback to prevent data changes where transactional semantics allow it
    timeout:=in.StatementTimeout;if timeout<=0{timeout=10*time.Second}
    // MySQL session timeout is expressed in milliseconds. Set a bounded session value.
    _,_ = tx.ExecContext(ctx,`SET SESSION MAX_EXECUTION_TIME = ?`,timeout.Milliseconds())
    out:=&dbms.ExplainResult{Statements:[]dbms.ExplainStatement{}}
    for _,s:=range stmts{
        k:=first(s.SQL);ok:=mysqlExplainable(k)
        item:=dbms.ExplainStatement{Index:s.Index,SQL:s.SQL,Explainable:ok}
        if !ok{
            if _,err=tx.ExecContext(ctx,s.SQL);err!=nil{return nil,fmt.Errorf("statement %d (context DDL): %w",s.Index,err)}
            item.Notice="DDL executed only to establish context; not explainable"
            out.Statements=append(out.Statements,item);continue
        }
        prefix:="EXPLAIN FORMAT=JSON "
        if in.Analyze{prefix="EXPLAIN ANALYZE "}
        rows,queryErr:=tx.QueryContext(ctx,prefix+s.SQL)
        if queryErr!=nil{return nil,fmt.Errorf("explain statement %d: %w",s.Index,queryErr)}
        cols,_:=rows.Columns()
        values:=make([]any,len(cols));ptrs:=make([]any,len(cols));for i:=range values{ptrs[i]=&values[i]}
        if rows.Next(){if err=rows.Scan(ptrs...);err!=nil{rows.Close();return nil,err}}
        rows.Close()
        var raw string
        if len(values)>0{switch v:=values[0].(type){case []byte:raw=string(v);case string:raw=v}}
        var plan any
        if in.Analyze { plan=map[string]any{"tree":raw} } else if err=json.Unmarshal([]byte(raw),&plan);err!=nil{return nil,fmt.Errorf("decode plan statement %d: %w",s.Index,err)}
        item.Plan=plan;out.Statements=append(out.Statements,item)
    }
    return out,nil
}
func first(s string)string{f:=strings.Fields(strings.TrimSpace(s));if len(f)==0{return ""};return strings.ToUpper(strings.Trim(f[0],"("))}
func mysqlExplainable(k string)bool{switch k{case "SELECT","INSERT","UPDATE","DELETE","REPLACE","WITH":return true;default:return false}}
