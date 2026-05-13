import { env } from '$env/dynamic/public';

export function load() {
  return {
    appName: env.PUBLIC_APP_NAME || 'StackKit Smoke'
  };
}
