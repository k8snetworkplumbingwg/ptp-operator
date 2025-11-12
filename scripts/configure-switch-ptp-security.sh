#!/bin/bash

set -e

echo "========================================="
echo "Configuring PTP Security on Switch1"
echo "========================================="

# ============================================
# CONFIGURATION - Edit these values as needed
# ============================================
SWITCH_CONTAINER="switch1"
NAMESPACE="openshift-ptp"
# --------------------------------------------
# CONFIGURATION - Edit these values as needed
# --------------------------------------------
SECRET_NAME="ptp-security-conf"
SA_FILE_PATH="/etc/ptp/ptp-security.conf"  # Where to write file in container
SPP_VALUE="1"
ACTIVE_KEY_ID="1"
# ============================================

# Step 1: Get secret and dynamically determine the key
echo "[1/3] Fetching secret '${SECRET_NAME}' from Kubernetes..."

SECRET_JSON=$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o json)
if [ $? -ne 0 ]; then
    echo "ERROR: Could not fetch secret '${SECRET_NAME}' from namespace '${NAMESPACE}'"
    exit 1
fi

# Dynamically get the first (or only) key from the secret
SECRET_KEY=$(echo "$SECRET_JSON" | jq -r '.data | keys[0]')
if [ -z "$SECRET_KEY" ] || [ "$SECRET_KEY" = "null" ]; then
    echo "ERROR: No keys found in secret '${SECRET_NAME}'"
    exit 1
fi

echo "Configuration:"
echo "  Switch: ${SWITCH_CONTAINER}"
echo "  Secret: ${SECRET_NAME}"
echo "  Secret Key: ${SECRET_KEY}"
echo "  SA File Path: ${SA_FILE_PATH}"
echo "  SPP: ${SPP_VALUE}"
echo "  Active Key ID: ${ACTIVE_KEY_ID}"
echo "========================================="
echo ""

# Get secret content using the detected key
SECRET_CONTENT=$(echo "$SECRET_JSON" | jq -r ".data[\"${SECRET_KEY}\"]" | base64 -d)

if [ -z "$SECRET_CONTENT" ]; then
    echo "ERROR: Could not retrieve secret key '${SECRET_KEY}' from secret '${SECRET_NAME}'"
    echo "Available keys in secret:"
    echo "$SECRET_JSON" | jq -r '.data | keys[]'
    exit 1
fi

echo "✓ Secret retrieved successfully ($(echo "$SECRET_CONTENT" | wc -l) lines)"
echo ""

# Step 2: Update /etc/ptp4l.conf in the switch container
echo "[2/3] Updating /etc/ptp4l.conf in ${SWITCH_CONTAINER}..."

# Create a script that will run inside the container
UPDATE_SCRIPT=$(cat <<'EOF_SCRIPT'
#!/bin/bash

SA_FILE_PATH="$1"
SPP_VALUE="$2"
ACTIVE_KEY_ID="$3"

PTP4L_CONF="/etc/ptp4l.conf"
BACKUP_FILE="/etc/ptp4l.conf.backup"

# Backup original config
cp "$PTP4L_CONF" "$BACKUP_FILE" 2>/dev/null || true

# Check if [global] section exists
if ! grep -q "^\[global\]" "$PTP4L_CONF"; then
    echo "ERROR: [global] section not found in $PTP4L_CONF"
    exit 1
fi

# Create temp file with updated config
# Strategy: Remove old auth settings, then add new ones
awk -v sa_file="$SA_FILE_PATH" -v spp="$SPP_VALUE" -v key_id="$ACTIVE_KEY_ID" '
BEGIN { in_global = 0; added = 0 }
{
    # Track if we are in [global] section
    if ($0 ~ /^\[global\]/) {
        in_global = 1
        print $0
        next
    }
    
    # Check if we left [global] section (entered a new section)
    if ($0 ~ /^\[.*\]/ && $0 !~ /^\[global\]/) {
        in_global = 0
    }
    
    # If in [global] section, skip existing auth lines (remove old values)
    if (in_global) {
        if ($0 ~ /^sa_file[ \t]/ || $0 ~ /^spp[ \t]/ || $0 ~ /^active_key_id[ \t]/) {
            next  # Skip this line (removes old value)
        }
        
        # Add new auth settings after first non-comment, non-empty line
        if (!added && $0 !~ /^#/ && $0 !~ /^$/) {
            print "sa_file " sa_file
            print "spp " spp
            print "active_key_id " key_id
            added = 1
        }
    }
    
    # Print the line (unless it was an old auth setting that we skipped)
    print $0
}
' "$PTP4L_CONF" > "${PTP4L_CONF}.new"

# Replace original with updated
mv "${PTP4L_CONF}.new" "$PTP4L_CONF"

echo "✓ Updated $PTP4L_CONF with security settings"
EOF_SCRIPT
)

# Execute the update script in the container
echo "$UPDATE_SCRIPT" | podman exec -i "${SWITCH_CONTAINER}" bash -s -- "${SA_FILE_PATH}" "${SPP_VALUE}" "${ACTIVE_KEY_ID}"

if [ $? -ne 0 ]; then
    echo "ERROR: Failed to update ptp4l.conf in ${SWITCH_CONTAINER}"
    exit 1
fi

echo "✓ ptp4l.conf updated successfully"
echo ""

# Step 3: Write secret content to sa_file path
echo "[3/3] Writing secret content to ${SA_FILE_PATH} in ${SWITCH_CONTAINER}..."

# Create directory if it doesn't exist
SA_FILE_DIR=$(dirname "${SA_FILE_PATH}")
podman exec "${SWITCH_CONTAINER}" mkdir -p "${SA_FILE_DIR}"

# Write secret content to the file
echo "$SECRET_CONTENT" | podman exec -i "${SWITCH_CONTAINER}" tee "${SA_FILE_PATH}" > /dev/null

if [ $? -ne 0 ]; then
    echo "ERROR: Failed to write secret to ${SA_FILE_PATH}"
    exit 1
fi

echo "✓ Secret written to ${SA_FILE_PATH}"
echo ""

# Verification
echo "========================================="
echo "✓ Configuration completed successfully!"
echo "========================================="

# Restart ptp4l to apply changes (full restart required)
echo ""
echo "Restarting ptp4l on ${SWITCH_CONTAINER}..."
podman exec "${SWITCH_CONTAINER}" systemctl restart ptp4l || {
    echo "WARNING: systemctl restart failed, trying pkill..."
    podman exec "${SWITCH_CONTAINER}" pkill ptp4l 2>/dev/null || true
    sleep 2
}

sleep 3
echo "✓ ptp4l restarted with authentication enabled"

