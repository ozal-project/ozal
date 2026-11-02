import type { Connector } from "./api";

export type ConnectorState = "revoked" | "connected" | "waiting" | "idle";

export function validConnectorName(value: string): boolean {
  return /^[a-z0-9]+(-[a-z0-9]+)*$/.test(value);
}

export function connectorCommandName(value: string): string {
  return validConnectorName(value) ? value : "NAME";
}

export function connectorState(connector: Connector): ConnectorState {
  return connector.revoked ? "revoked" : connector.connected ? "connected" : connector.hosts.length ? "waiting" : "idle";
}

export function connectorDaysLeft(connector: Connector, now = Date.now()): number | null {
  if (!connector.not_after) return null;
  const expiry = new Date(connector.not_after).getTime();
  return Number.isFinite(expiry) ? Math.floor((expiry - now) / 86_400_000) : null;
}

export function connectorProgress(connector?: Connector): number {
  return connector ? connector.connected ? 5 : 4 : 0;
}
