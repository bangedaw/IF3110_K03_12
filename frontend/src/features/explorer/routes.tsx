import type { RouteObject } from "react-router-dom"
import { SchemaDiffPage } from "@/features/schema-diff/SchemaDiffPage"
import { DatabaseLayout, ExplorerHome } from "./DatabaseLayout"
import { DatabaseListPage } from "./DatabaseListPage"
import { TableView } from "./TableView"

export const explorerRoutes: RouteObject[] = [
  { path: "databases", element: <DatabaseListPage /> },
  {
    path: "databases/:databaseId",
    element: <DatabaseLayout />,
    children: [
      { index: true, element: <ExplorerHome /> },
      { path: "schemas/:schema/tables/:table", element: <TableView /> },
      { path: "diff", element: <SchemaDiffPage /> },
    ],
  },
]
