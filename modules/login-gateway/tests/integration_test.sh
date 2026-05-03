#!/usr/bin/env bash
# Integration test for login-gateway module.
#
# Scaffold for V6 Phase 2. Full test includes:
# - Deploy Traefik + TinyAuth + PocketID + LLDAP + a dummy L3 service.
# - Request https://dummy.<domain> unauthenticated → expect 302 to auth.<domain>.
# - Follow redirect → expect TinyAuth login page.
# - Complete OIDC flow → expect redirect back to dummy service with session cookie.
# - Verify dummy service receives X-Forwarded-User header.
# - Verify a service annotated with exposed-to-public bypasses login-gateway only
#   if it is on the allowedPublicBypass list (reject otherwise).
#
# STATUS: placeholder.

set -euo pipefail

echo "login-gateway integration test — scaffolded, not yet implemented."
echo "Phase 2 (V6, Q3/2026) will fill in the test cases."
exit 0
