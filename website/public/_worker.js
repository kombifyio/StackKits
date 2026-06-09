const installerHosts = new Map([
  ["install.stackkit.cc", "/install"],
  ["base.stackkit.cc", "/base"],
  ["modern.stackkit.cc", "/modern"],
  ["ha.stackkit.cc", "/ha"],
]);

const installerPaths = new Set(["/install", "/base", "/modern", "/ha"]);

const embedFrameAncestors = "frame-ancestors 'self' https://kombify.io";

const siteHeaders = {
  "Content-Security-Policy": embedFrameAncestors,
  "Referrer-Policy": "strict-origin-when-cross-origin",
  "X-Content-Type-Options": "nosniff",
};

const shellHeaders = {
  ...siteHeaders,
  "Content-Type": "text/x-shellscript; charset=utf-8",
  "Cache-Control": "public, max-age=300",
};

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const hostPath = installerHosts.get(url.hostname.toLowerCase());

    if (hostPath && (url.pathname === "/" || url.pathname === "")) {
      return serveInstallerAsset(request, env, hostPath);
    }

    if (installerPaths.has(url.pathname)) {
      return serveInstallerAsset(request, env, url.pathname);
    }

    return withSiteHeaders(await env.ASSETS.fetch(request));
  },
};

async function serveInstallerAsset(request, env, pathname) {
  const assetURL = new URL(request.url);
  assetURL.pathname = pathname;
  assetURL.search = "";

  const assetRequest = new Request(assetURL.toString(), request);
  const assetResponse = await env.ASSETS.fetch(assetRequest);
  const response = withSiteHeaders(assetResponse);

  for (const [name, value] of Object.entries(shellHeaders)) {
    response.headers.set(name, value);
  }

  return response;
}

function withSiteHeaders(assetResponse) {
  const response = new Response(assetResponse.body, assetResponse);
  response.headers.delete("X-Frame-Options");

  for (const [name, value] of Object.entries(siteHeaders)) {
    response.headers.set(name, value);
  }

  return response;
}
