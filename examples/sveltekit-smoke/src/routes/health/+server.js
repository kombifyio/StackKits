import { env } from '$env/dynamic/public';

export function GET() {
  return new Response(JSON.stringify({ status: 'ok', app: env.PUBLIC_APP_NAME || 'StackKit Smoke' }), {
    headers: {
      'content-type': 'application/json'
    }
  });
}
