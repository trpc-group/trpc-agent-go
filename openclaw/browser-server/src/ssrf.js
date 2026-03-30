import dns from "node:dns/promises";
import net from "node:net";

function normalizeDomain(value) {
  return `${value || ""}`.trim().toLowerCase().replace(/\.$/, "");
}

function parseDomains(value) {
  return `${value || ""}`
    .split(",")
    .map(normalizeDomain)
    .filter(Boolean);
}

function isLoopbackHost(host) {
  return host === "localhost" || host.endsWith(".localhost");
}

function isPrivateIPv4(address) {
  return (
    address.startsWith("10.") ||
    address.startsWith("192.168.") ||
    /^172\.(1[6-9]|2\d|3[0-1])\./.test(address) ||
    address.startsWith("169.254.") ||
    address === "0.0.0.0"
  );
}

function isPrivateIPv6(address) {
  const value = address.toLowerCase();
  return (
    value === "::" ||
    value === "::1" ||
    value.startsWith("fc") ||
    value.startsWith("fd") ||
    value.startsWith("fe80:")
  );
}

function isPrivateAddress(address) {
  if (net.isIPv4(address)) {
    return isPrivateIPv4(address);
  }
  if (net.isIPv6(address)) {
    return isPrivateIPv6(address);
  }
  return false;
}

function hostMatchesDomain(host, domain) {
  return host === domain || host.endsWith(`.${domain}`);
}

export function createNavigationPolicy(env = process.env) {
  return {
    allowedDomains: parseDomains(env.OPENCLAW_BROWSER_ALLOWED_DOMAINS),
    blockedDomains: parseDomains(env.OPENCLAW_BROWSER_BLOCKED_DOMAINS),
    allowLoopback:
      `${env.OPENCLAW_BROWSER_ALLOW_LOOPBACK || ""}` === "true",
    allowPrivateNetworks:
      `${env.OPENCLAW_BROWSER_ALLOW_PRIVATE_NETWORKS || ""}` === "true",
    allowFileURLs:
      `${env.OPENCLAW_BROWSER_ALLOW_FILE_URLS || ""}` === "true"
  };
}

export async function validateNavigationURL(rawURL, policy) {
  if (!rawURL) {
    return;
  }

  const url = new URL(rawURL);
  if (url.protocol === "about:") {
    return;
  }
  if (url.protocol === "file:") {
    if (policy.allowFileURLs) {
      return;
    }
    throw new Error(`Blocked file URL: ${rawURL}`);
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error(`Blocked URL scheme: ${url.protocol}`);
  }

  const host = normalizeDomain(url.hostname);
  if (!host) {
    return;
  }

  if (isLoopbackHost(host) && !policy.allowLoopback) {
    throw new Error(`Blocked loopback host: ${host}`);
  }

  const looksLikeIP = net.isIP(host) !== 0;
  const addrs = looksLikeIP ? [{ address: host }] : await dns.lookup(host, {
    all: true
  });

  for (const addr of addrs) {
    if (addr.address === "::1" && !policy.allowLoopback) {
      throw new Error(`Blocked loopback address: ${addr.address}`);
    }
    if (addr.address === "127.0.0.1" && !policy.allowLoopback) {
      throw new Error(`Blocked loopback address: ${addr.address}`);
    }
    if (isPrivateAddress(addr.address) && !policy.allowPrivateNetworks) {
      throw new Error(`Blocked private address: ${addr.address}`);
    }
  }

  for (const domain of policy.blockedDomains) {
    if (hostMatchesDomain(host, domain)) {
      throw new Error(`Blocked domain: ${host}`);
    }
  }

  if (policy.allowedDomains.length === 0) {
    return;
  }
  for (const domain of policy.allowedDomains) {
    if (hostMatchesDomain(host, domain)) {
      return;
    }
  }
  throw new Error(`Domain is not allowed: ${host}`);
}
