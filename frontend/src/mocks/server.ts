import { setupServer } from "msw/node"
import { connectionHandlers } from "./connections"
import { explorerHandlers } from "./explorer"

export const server = setupServer(...connectionHandlers, ...explorerHandlers)
