package migration

import "context"

type ReconcileRepository interface {
    StaleRunning(ctx context.Context, olderThanSeconds int)([]Run,error)
    MarkReconciled(ctx context.Context,runID int64,status Status,version *int,reason string)error
}
type TargetHistory interface {
    HasCommittedRun(ctx context.Context,databaseID,runID int64)(version int,checksum string,found bool,err error)
}
type Reconciler struct { Repo ReconcileRepository; Targets TargetHistory; StaleAfterSeconds int }
func (r Reconciler) Reconcile(ctx context.Context)error{
    stale:=r.StaleAfterSeconds;if stale<=0{stale=60}
    runs,err:=r.Repo.StaleRunning(ctx,stale);if err!=nil{return err}
    for _,run:=range runs{
        version,checksum,found,checkErr:=r.Targets.HasCommittedRun(ctx,run.DatabaseID,run.ID)
        if checkErr!=nil { continue }  
        if found{
            _=checksum 
            if err=r.Repo.MarkReconciled(ctx,run.ID,RunSucceeded,&version,"target history confirms commit");err!=nil{return err}
        }else{
            if err=r.Repo.MarkReconciled(ctx,run.ID,RunFailed,nil,"no committed target history row found after stale running state");err!=nil{return err}
        }
    }
    return nil
}
