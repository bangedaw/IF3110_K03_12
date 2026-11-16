import { http, HttpResponse } from "msw";

const drafts = [
  {id:1,databaseId:1,title:"Add example table",description:"MSW sample",sql:"CREATE TABLE example (id integer PRIMARY KEY);",checksum:"mock",status:"draft",baseVersion:0,revision:1,createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()},
];
const runs: unknown[] = [];
export const migrationHandlers = [
  http.get("/api/databases/:databaseId/migrations",({request})=>{
    const status=new URL(request.url).searchParams.get("status");
    return HttpResponse.json({items:status==="applied"?[]:drafts,limit:50,offset:0});
  }),
  http.post("/api/databases/:databaseId/migrations",async({request})=>{
    const body=await request.json() as Record<string,unknown>;
    const item={...drafts[0],...body,id:Date.now(),revision:1,status:"draft"};
    drafts.unshift(item as typeof drafts[number]);
    return HttpResponse.json(item,{status:201});
  }),
  http.get("/api/databases/:databaseId/migrations/:migrationId",({params})=>{
    const item=drafts.find(x=>x.id===Number(params.migrationId));
    return item?HttpResponse.json(item):HttpResponse.json({detail:"Migration not found"},{status:404});
  }),
  http.patch("/api/databases/:databaseId/migrations/:migrationId",async({params,request})=>{
    const idx=drafts.findIndex(x=>x.id===Number(params.migrationId));
    if(idx<0)return HttpResponse.json({detail:"Migration not found"},{status:404});
    const body=await request.json() as Record<string,unknown>;
    if(body.revision!==drafts[idx].revision)return HttpResponse.json({detail:"Revision conflict"},{status:409});
    drafts[idx]={...drafts[idx],...body,revision:drafts[idx].revision+1,updatedAt:new Date().toISOString()};
    return HttpResponse.json(drafts[idx]);
  }),
  http.post("/api/databases/:databaseId/migrations/:migrationId/execute",()=>{
    const run={id:Date.now(),migrationId:1,databaseId:1,status:"succeeded",startedAt:new Date().toISOString()};
    runs.unshift(run);return HttpResponse.json(run);
  }),
  http.get("/api/databases/:databaseId/history",()=>HttpResponse.json({items:runs,limit:50,offset:0})),
  http.get("/api/databases/:databaseId/migrations/:migrationId/runs",()=>HttpResponse.json(runs)),
  http.post("/api/databases/:databaseId/migrations/:migrationId/explain",()=>HttpResponse.json({statements:[]})),
];
