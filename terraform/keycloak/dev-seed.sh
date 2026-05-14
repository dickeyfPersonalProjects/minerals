#!/usr/bin/env bash
# Dev-only Keycloak realm seeder.
#
# Brings the local dev Keycloak realm + a known set of test users to a
# usable state with one command. Idempotent — safe to re-run.
#
#   docker compose --profile keycloak up -d keycloak
#   bash terraform/keycloak/dev-seed.sh
#
# Prereqs: terraform, curl, jq.

set -euo pipefail

KEYCLOAK_URL=${KEYCLOAK_URL:-http://localhost:8081}
KC_ADMIN_USER=${KC_ADMIN_USER:-admin}
KC_ADMIN_PASS=${KC_ADMIN_PASS:-admin}
REALM=${REALM:-minerals}

# Realm enforces `length(12) and notUsername and notEmail`, so password=username
# (per the original bead recommendation) is rejected. Use a known dev-only value.
DEV_PASSWORD=${DEV_PASSWORD:-MineralsDev123!}

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

for bin in terraform curl jq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "missing required binary: $bin" >&2; exit 1; }
done

echo "==> terraform init"
terraform init -input=false >/dev/null

echo "==> terraform apply -var-file=dev.tfvars"
terraform apply -var-file=dev.tfvars -auto-approve

REALM_ISSUER=$(terraform output -raw realm_issuer)
FRONTEND_CLIENT_ID=$(terraform output -raw frontend_client_id)
BACKEND_CLIENT_ID=$(terraform output -raw backend_client_id)

echo "==> obtaining admin token via master realm"
TOKEN=$(curl -fsS -X POST \
  "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
  -d "grant_type=password" \
  -d "client_id=admin-cli" \
  -d "username=${KC_ADMIN_USER}" \
  -d "password=${KC_ADMIN_PASS}" \
  | jq -r .access_token)

if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
  echo "failed to obtain admin token from ${KEYCLOAK_URL}/realms/master" >&2
  exit 1
fi

AUTH=(-H "Authorization: Bearer ${TOKEN}")
JSON=(-H "Content-Type: application/json")

# Echo the user id for a given username, or empty string if not found.
user_id() {
  local username=$1
  curl -fsS "${AUTH[@]}" \
    "${KEYCLOAK_URL}/admin/realms/${REALM}/users?username=${username}&exact=true" \
    | jq -r '.[0].id // empty'
}

# Idempotent create: if the username exists, return its id; otherwise POST a
# new user. 409 is treated as a successful no-op (race or stale read).
create_user() {
  local username=$1
  local id
  id=$(user_id "$username")
  if [[ -n "$id" ]]; then
    echo "$id"
    return
  fi

  local body
  body=$(jq -n \
    --arg u "$username" \
    --arg e "${username}@localhost" \
    --arg p "$DEV_PASSWORD" \
    '{
       username: $u,
       email: $e,
       enabled: true,
       emailVerified: true,
       credentials: [{type: "password", value: $p, temporary: false}]
     }')

  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' "${AUTH[@]}" "${JSON[@]}" \
    -X POST "${KEYCLOAK_URL}/admin/realms/${REALM}/users" \
    -d "$body")
  if [[ "$code" != "201" && "$code" != "409" ]]; then
    echo "create user $username failed: HTTP $code" >&2
    exit 1
  fi

  user_id "$username"
}

# Assign a realm role to a user. Re-assigning an existing role is a no-op.
assign_realm_role() {
  local uid=$1
  local role_name=$2

  local role_payload
  role_payload=$(curl -fsS "${AUTH[@]}" \
    "${KEYCLOAK_URL}/admin/realms/${REALM}/roles/${role_name}" \
    | jq -c '[{id: .id, name: .name}]')

  curl -fsS -o /dev/null "${AUTH[@]}" "${JSON[@]}" \
    -X POST "${KEYCLOAK_URL}/admin/realms/${REALM}/users/${uid}/role-mappings/realm" \
    -d "$role_payload"
}

echo "==> seeding test users"

for u in user1 user2 user3 user4 user5; do
  id=$(create_user "$u")
  echo "    + $u ($id)"
done

for pair in "devops_viewer_user:devops-viewer" "devops_admin_user:devops-admin"; do
  user="${pair%%:*}"
  role="${pair##*:}"
  id=$(create_user "$user")
  assign_realm_role "$id" "$role"
  echo "    + $user ($id) -> role:$role"
done

cat <<EOF

Realm:           ${REALM}
Issuer:          ${REALM_ISSUER}
Frontend client: ${FRONTEND_CLIENT_ID}
Backend client:  ${BACKEND_CLIENT_ID}

Test users (password = ${DEV_PASSWORD}):
  user1, user2, user3, user4, user5     (no roles)
  devops_viewer_user                    (devops-viewer)
  devops_admin_user                     (devops-admin)
EOF
