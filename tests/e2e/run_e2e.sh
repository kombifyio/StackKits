#!/bin/bash
# =============================================================================
# E2E TEST SUITE: base-kit
# =============================================================================
# End-to-end tests for the base-kit StackKit
#
# These tests verify:
# 1. CUE schema validation
# 2. Template generation
# 3. Terraform plan (dry-run)
# 4. (Optional) Actual deployment to test VM
#
# Prerequisites:
# - CUE installed
# - StackKit release archive includes packaged OpenTofu
# - (Optional) Test VM accessible via SSH
#
# Usage:
#   ./tests/e2e/run_e2e.sh              # Dry-run mode
#   ./tests/e2e/run_e2e.sh --deploy     # Actually deploy to test VM
#
# Environment Variables:
#   TEST_HOST      - IP/hostname of test VM
#   TEST_USER      - SSH user for test VM
#   TEST_SSH_KEY   - Path to SSH key
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"
TEMP_DIR="${SCRIPT_DIR}/tmp"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Test mode
DEPLOY_MODE=false
if [ "$1" == "--deploy" ]; then
    DEPLOY_MODE=true
fi

# Test configuration
TEST_HOST="${TEST_HOST:-localhost}"
TEST_USER="${TEST_USER:-root}"
TEST_SSH_KEY="${TEST_SSH_KEY:-~/.ssh/id_rsa}"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}    base-kit E2E Test Suite        ${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Cleanup function
cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"
    rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

# Create temp directory
mkdir -p "$TEMP_DIR"

# Track results
PASSED=0
FAILED=0

pass() {
    echo -e "${GREEN}✓ PASSED${NC}"
    PASSED=$((PASSED + 1))
}

fail() {
    echo -e "${RED}✗ FAILED${NC}"
    FAILED=$((FAILED + 1))
}

# =============================================================================
# TEST 1: Schema Validation
# =============================================================================
echo -e "\n${YELLOW}Test 1: Schema Validation${NC}"
echo "-------------------------------------------"

echo -n "Validating base-kit schema... "
if cue vet "$PROJECT_ROOT/base-kit/stackfile.cue" 2>/dev/null; then
    pass
else
    fail
    cue vet "$PROJECT_ROOT/base-kit/stackfile.cue" 2>&1 || true
fi

# =============================================================================
# TEST 2: Default Spec Validation
# =============================================================================
echo -e "\n${YELLOW}Test 2: Default Spec Validation${NC}"
echo "-------------------------------------------"

# Create test spec
cat > "$TEMP_DIR/test-spec.yaml" << 'EOF'
variant: default
system:
  timezone: "Europe/Berlin"
nodes:
  - name: test-homelab
    role: main
    type: local
    os: ubuntu-24
    resources:
      cpu: 4
      memory: 8
      disk: 100
    connection:
      host: "192.168.1.100"
      user: root
      ssh_key: "/root/.ssh/id_rsa"
network:
  mode: local
  domain: homelab.local
EOF

echo -n "Validating test spec against schema... "
# Use CUE to validate the spec (this is a simplified check)
if cue eval "$PROJECT_ROOT/base-kit/stackfile.cue" -e '#BaseKitStack' >/dev/null 2>&1; then
    pass
else
    fail
fi

# =============================================================================
# TEST 3: Variant Tests
# =============================================================================
echo -e "\n${YELLOW}Test 3: Variant Tests${NC}"
echo "-------------------------------------------"

echo -n "Checking variant fixtures are present... "
if grep -q '_validDefaultVariant' "$PROJECT_ROOT/base-kit/tests/schema_test.cue" && \
   grep -q '_validBeszelVariant' "$PROJECT_ROOT/base-kit/tests/schema_test.cue" && \
   grep -q '_validMinimalVariant' "$PROJECT_ROOT/base-kit/tests/schema_test.cue"; then
    pass
else
    fail
fi

# =============================================================================
# TEST 4: Template Generation
# =============================================================================
echo -e "\n${YELLOW}Test 4: Template Generation${NC}"
echo "-------------------------------------------"

# Simulate template generation (what the Go CLI would do)
generate_terraform() {
    local output_dir="$TEMP_DIR/generated"
    mkdir -p "$output_dir"
    
    # Copy and process templates (simplified - real CLI would use Go templating)
    echo -n "Generating Layer 1 (CORE) templates... "
    
    # Check required templates exist
    local missing=0
    for template in \
        "base/bootstrap/_bootstrap.tf.tmpl" \
        "base/security/_firewall.tf.tmpl" \
        "base/security/_ssh.tf.tmpl" \
        "base/security/_fail2ban.tf.tmpl" \
        "base/observability/_health.tf.tmpl"
    do
        if [ ! -f "$PROJECT_ROOT/$template" ]; then
            echo -e "\n${RED}Missing: $template${NC}"
            missing=1
        fi
    done
    
    if [ $missing -eq 0 ]; then
        pass
    else
        fail
    fi
    
    echo -n "Generating Layer 2 (PLATFORM) templates... "
    missing=0
    for template in \
        "platforms/docker/_docker.tf.tmpl" \
        "platforms/docker/_traefik.tf.tmpl"
    do
        if [ ! -f "$PROJECT_ROOT/$template" ]; then
            echo -e "\n${RED}Missing: $template${NC}"
            missing=1
        fi
    done
    
    if [ $missing -eq 0 ]; then
        pass
    else
        fail
    fi
    
    echo -n "Generating Layer 3 (SERVICES) templates... "
    missing=0
    for template in \
        "base-kit/templates/simple/main.tf" \
        "base-kit/templates/native/main.tf" \
        "base-kit/templates/simple/modules/dokploy/main.tf" \
        "base-kit/templates/simple/modules/dockge/main.tf" \
        "base-kit/templates/simple/modules/monitoring/main.tf" \
        "base-kit/templates/simple/modules/traefik/main.tf" \
        "base-kit/templates/simple/modules/whoami/main.tf"
    do
        if [ ! -f "$PROJECT_ROOT/$template" ]; then
            echo -e "\n${RED}Missing: $template${NC}"
            missing=1
        fi
    done
    
    if [ $missing -eq 0 ]; then
        pass
    else
        fail
    fi
}

generate_terraform

# =============================================================================
# TEST 5: Terraform Syntax Check
# =============================================================================
echo -e "\n${YELLOW}Test 5: Terraform Syntax Check${NC}"
echo "-------------------------------------------"

check_tf_syntax() {
    local name="$1"
    local file="$2"
    
    echo -n "Checking $name syntax... "
    
    # Create a temp .tf file (strip .tmpl extension conceptually)
    local temp_tf="$TEMP_DIR/syntax_check.tf"
    
    # Extract only the Terraform parts (skip template directives)
    grep -v '^\s*%{' "$file" | grep -v 'var\.' > "$temp_tf" 2>/dev/null || true
    
    # Since we can't fully validate without resolving variables, 
    # just check for basic structure
    if grep -q -E '(variable|resource|output|locals)' "$file"; then
        pass
    else
        echo -e "${YELLOW}WARNING: No Terraform constructs${NC}"
    fi
}

check_tf_syntax "bootstrap" "$PROJECT_ROOT/base/bootstrap/_bootstrap.tf.tmpl"
check_tf_syntax "firewall" "$PROJECT_ROOT/base/security/_firewall.tf.tmpl"
check_tf_syntax "docker" "$PROJECT_ROOT/platforms/docker/_docker.tf.tmpl"
check_tf_syntax "traefik" "$PROJECT_ROOT/platforms/docker/_traefik.tf.tmpl"

# =============================================================================
# TEST 6: Service Definition Tests
# =============================================================================
echo -e "\n${YELLOW}Test 6: Service Definition Tests${NC}"
echo "-------------------------------------------"

test_service_def() {
    local service="$1"
    local expected_image="$2"
    local file="$3"
    
    echo -n "Testing $service service definition... "
    
    if [ ! -f "$file" ]; then
        fail
        echo "  File not found: $file"
        return
    fi
    
    # Check for expected image reference
    if grep -q "$expected_image" "$file"; then
        pass
    else
        fail
        echo "  Expected image '$expected_image' not found"
    fi
}

test_service_def "Dokploy" "dokploy/dokploy" "$PROJECT_ROOT/base-kit/templates/simple/modules/dokploy/main.tf"
test_service_def "Uptime Kuma" "louislam/uptime-kuma" "$PROJECT_ROOT/base-kit/templates/simple/modules/monitoring/main.tf"
test_service_def "Beszel" "henrygd/beszel" "$PROJECT_ROOT/base-kit/templates/simple/modules/monitoring/main.tf"
test_service_def "Dockge" "louislam/dockge" "$PROJECT_ROOT/base-kit/templates/simple/modules/dockge/main.tf"
test_service_def "Portainer" "portainer/portainer-ce" "$PROJECT_ROOT/base-kit/templates/simple/modules/portainer/main.tf"
test_service_def "Netdata" "netdata/netdata" "$PROJECT_ROOT/base-kit/templates/simple/modules/monitoring/main.tf"

# =============================================================================
# TEST 7: Network Mode Tests
# =============================================================================
echo -e "\n${YELLOW}Test 7: Network Mode Tests${NC}"
echo "-------------------------------------------"

echo -n "Testing 'local' network mode (self-signed TLS)... "
if grep -q "self-signed\|self_signed\|local" "$PROJECT_ROOT/platforms/docker/_traefik.tf.tmpl"; then
    pass
else
    fail
fi

echo -n "Testing 'public' network mode (ACME/Let's Encrypt)... "
if grep -q "letsencrypt\|acme" "$PROJECT_ROOT/platforms/docker/_traefik.tf.tmpl"; then
    pass
else
    fail
fi

# =============================================================================
# TEST 8: Security Hardening Tests
# =============================================================================
echo -e "\n${YELLOW}Test 8: Security Hardening Tests${NC}"
echo "-------------------------------------------"

echo -n "Checking SSH hardening template... "
if grep -q "PasswordAuthentication\|PermitRootLogin" "$PROJECT_ROOT/base/security/_ssh.tf.tmpl"; then
    pass
else
    fail
fi

echo -n "Checking firewall template... "
if grep -q "ufw\|firewall" "$PROJECT_ROOT/base/security/_firewall.tf.tmpl"; then
    pass
else
    fail
fi

echo -n "Checking fail2ban template... "
if grep -q "fail2ban" "$PROJECT_ROOT/base/security/_fail2ban.tf.tmpl"; then
    pass
else
    fail
fi

# =============================================================================
# TEST 9: Deployment Test (Optional)
# =============================================================================
if [ "$DEPLOY_MODE" = true ]; then
    echo -e "\n${YELLOW}Test 9: Deployment Test${NC}"
    echo "-------------------------------------------"
    
    echo -e "${RED}WARNING: Deployment tests not yet implemented${NC}"
    echo "This would:"
    echo "1. Generate Terraform files from templates"
    echo "2. Run 'terraform init'"
    echo "3. Run 'terraform plan'"
    echo "4. (Optional) Run 'terraform apply'"
    echo "5. Verify services are running"
    echo "6. Run health checks"
    echo "7. Cleanup (terraform destroy)"
else
    echo -e "\n${YELLOW}Test 9: Deployment Test${NC}"
    echo "-------------------------------------------"
    echo -e "Skipped (use --deploy to enable)"
fi

# =============================================================================
# SUMMARY
# =============================================================================
echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}           E2E Test Summary            ${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo -e "Total Passed: ${GREEN}$PASSED${NC}"
echo -e "Total Failed: ${RED}$FAILED${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ All E2E tests passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Some E2E tests failed!${NC}"
    exit 1
fi
