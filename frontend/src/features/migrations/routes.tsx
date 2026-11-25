import type { RouteObject } from "react-router-dom"
import { HistoryPage } from "./HistoryPage"
import { MigrationDetailPage } from "./MigrationDetailPage"
import { MigrationNewPage, MigrationsPage } from "./MigrationsPage"

// Dipasang sebagai anak route databases/:databaseId.
export const migrationRoutes: RouteObject[] = [
  { path: "migrations", element: <MigrationsPage /> },
  { path: "migrations/new", element: <MigrationNewPage /> },
  { path: "migrations/:migrationId", element: <MigrationDetailPage /> },
  { path: "history", element: <HistoryPage /> },
]
