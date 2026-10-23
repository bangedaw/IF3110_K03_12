import { setupWorker } from "msw/browser";
import { migrationHandlers } from "./migrations";

export const worker = setupWorker(...migrationHandlers);
