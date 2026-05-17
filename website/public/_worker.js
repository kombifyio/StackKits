const installerHosts = new Map([
  ["install.stackkit.cc", "/install"],
  ["base.stackkit.cc", "/base"],
  ["modern.stackkit.cc", "/modern"],
  ["ha.stackkit.cc", "/ha"],
]);

const installerPaths = new Set(["/install", "/base", "/modern", "/ha"]);

const shellHeaders = {
  "Content-Type": "text/x-shellscript; charset=utf-8",
  "X-Content-Type-Options": "nosniff",
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

    return env.ASSETS.fetch(request);
  },
};

async function serveInstallerAsset(request, env, pathname) {
  const assetURL = new URL(request.url);
  assetURL.pathname = pathname;
  assetURL.search = "";

  const assetRequest = new Request(assetURL.toString(), request);
  const assetResponse = await env.ASSETS.fetch(assetRequest);
  const response = new Response(assetResponse.body, assetResponse);

  for (const [name, value] of Object.entries(shellHeaders)) {
    response.headers.set(name, value);
  }

  return response;
}
