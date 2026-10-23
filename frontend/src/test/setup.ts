import "@testing-library/jest-dom/vitest"
import { cleanup } from "@testing-library/react"
import { afterAll, afterEach, beforeAll } from "vitest"
import { resetConnections } from "@/mocks/connections"
import { resetExplorerLog } from "@/mocks/explorer"
import { server } from "@/mocks/server"

const emptyRects = Object.assign([], { item: () => null })
const zeroRect = {
  x: 0,
  y: 0,
  width: 0,
  height: 0,
  top: 0,
  right: 0,
  bottom: 0,
  left: 0,
  toJSON: () => ({}),
}

Range.prototype.getClientRects = () => emptyRects as unknown as DOMRectList
Range.prototype.getBoundingClientRect = () => zeroRect as DOMRect

beforeAll(() => server.listen({ onUnhandledFrame: "error" }))

afterEach(() => {
  cleanup()
  server.resetHandlers()
  resetConnections()
  resetExplorerLog()
  localStorage.clear()
})

afterAll(() => server.close())
