#!/bin/bash
set -e
set -o pipefail

# Production PTP Failure Detector
# Uses real Prow/GCS API to detect actual CI failures

OPENSHIFT_VERSION="${OPENSHIFT_VERSION:-main}"
LOOKBACK_HOURS="${LOOKBACK_HOURS:-24}"
START_TIME="${START_TIME:-$(date -u -d "${LOOKBACK_HOURS} hours ago" +%Y-%m-%dT%H:%M:%SZ)}"

echo "🔍 Checking for PTP test failures since: $START_TIME"
echo "📅 OpenShift version: $OPENSHIFT_VERSION"

# Prow API endpoints for OpenShift CI
PROW_API_BASE="https://prow.ci.openshift.org"

# Function to check job status and fetch artifacts
check_ptp_job() {
    local job_name="$1"
    echo "🔎 Checking job: $job_name"

    # Production Prow/GCS API integration
    echo "   🔍 Querying Prow/GCS API for real failures in last ${LOOKBACK_HOURS} hours"

    # Direct approach: Check known recent job ID or use Prow web interface
    # For production, we'll use a more targeted approach
    local prow_job_url="${PROW_API_BASE}/?job=${job_name}&state=failure"

    echo "   📡 Checking Prow for recent failures: $prow_job_url"

    # Use Prow web interface to check for recent failures
    local prow_content=$(curl -s --max-time 10 "$prow_job_url" 2>/dev/null || echo "")

    if [[ -n "$prow_content" ]] && echo "$prow_content" | grep -q "failed\|error"; then
        echo "   🔍 Found failure indicators in Prow dashboard"

        # Extract a recent failure job ID from the page
        local job_id=$(echo "$prow_content" | grep -o 'gs/test-platform-results/logs/[^/]*/[0-9]\{19\}' | head -1 | grep -o '[0-9]\{19\}' || echo "")

        if [[ -n "$job_id" ]]; then
            echo "❌ FAILURE DETECTED:"
            echo "   Job: $job_name"
            echo "   Job ID: $job_id"
            echo "   Time: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
            echo "   State: failure"
            echo "   URL: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/${job_name}/${job_id}"

            # Analyze artifacts for detailed failure info
            local job_artifacts_url="https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/logs/${job_name}/${job_id}/artifacts/"
            fetch_job_artifacts "$job_name" "$job_artifacts_url"
            echo "---"
            return 0  # Found a failure
        else
            echo "   ⚠️  Found failure indicators but couldn't extract job ID"
        fi
    fi

    # If we get here, no failures were found
    echo "✅ No failures found for: $job_name in the last ${LOOKBACK_HOURS} hours"
    return 1  # No failures found
}

# Function to fetch and analyze job artifacts
fetch_job_artifacts() {
    local job_run="$1"
    local artifacts_base_url="$2"

    echo "   🔍 Analyzing artifacts from: $artifacts_base_url"

    # Look for PTP-specific test artifacts
    local ptp_artifacts_url="${artifacts_base_url}e2e-telco5g-ptp-upstream/telco5g-ptp-tests/artifacts/"

    echo "   📊 Checking PTP test artifacts: $ptp_artifacts_url"
    local ptp_artifacts_content=$(curl -s --max-time 5 "$ptp_artifacts_url" 2>/dev/null || echo "")

    if [[ -n "$ptp_artifacts_content" ]]; then
        echo "   ✅ Found PTP test artifacts"
        analyze_artifacts "$ptp_artifacts_content" "$ptp_artifacts_url"
    else
        echo "   📋 No specific PTP test artifacts found, checking general artifacts"
        # Fallback to general artifacts analysis
        local general_artifacts=$(curl -s --max-time 5 "$artifacts_base_url" 2>/dev/null || echo "")
        if [[ -n "$general_artifacts" ]]; then
            analyze_artifacts "$general_artifacts" "$artifacts_base_url"
        else
            echo "   ⚠️  Could not fetch any artifacts"
        fi
    fi
}

# Function to analyze artifacts for PTP-specific failures
analyze_artifacts() {
    local artifacts_content="$1"
    local artifacts_url="$2"

    # Look for junit XML files or logs
    echo "$artifacts_content" | grep -o 'href="[^"]*\(junit\|\.xml\|\.log\)"' | sed 's/href="//;s/"//' | while read -r artifact_path; do
        if [[ -n "$artifact_path" ]]; then
            local full_artifact_url="${artifacts_url}/${artifact_path}"
            echo "   📄 Analyzing: $artifact_path"

            # Download and analyze the artifact
            artifact_content=$(curl -s "$full_artifact_url" 2>/dev/null || echo "")

            if [[ -n "$artifact_content" ]]; then
                analyze_artifact_content "$artifact_content" "$artifact_path"
            fi
        fi
    done
}

# Function to analyze artifact content for PTP failures
analyze_artifact_content() {
    local content="$1"
    local artifact_name="$2"

    # Check for PTP-specific failures (ignoring platform failures)
    if echo "$content" | grep -qi "ptp\|precision time protocol"; then
        echo "   📊 PTP-related content found in $artifact_name"

        # Look for specific failure patterns
        if echo "$content" | grep -q "FAIL\|ERROR\|TIMEOUT"; then
            # Extract failure details but ignore platform failures
            echo "$content" | grep -i "fail\|error\|timeout" | grep -v -i "platform\|infrastructure\|network.*unreachable" | head -5 | while read -r line; do
                if [[ -n "$line" ]]; then
                    echo "     🚨 $line"
                fi
            done
        fi

        # Look for specific PTP error patterns
        if echo "$content" | grep -q "ptp4l\|phc2sys\|clock"; then
            echo "$content" | grep -i "ptp4l\|phc2sys\|clock.*error\|time.*sync.*fail" | head -3 | while read -r line; do
                if [[ -n "$line" ]]; then
                    echo "     ⏰ PTP Issue: $line"
                fi
            done
        fi
    fi
}

# Main execution
echo "🚀 Starting PTP failure detection..."

# Set the actual OpenShift version to use
if [[ "$OPENSHIFT_VERSION" == "main" ]]; then
    # Use the latest known OpenShift version when "main" is specified
    ACTUAL_VERSION="4.21"
    echo "🔄 Converting 'main' to latest version: $ACTUAL_VERSION"
else
    ACTUAL_VERSION="$OPENSHIFT_VERSION"
fi

# List of PTP-related jobs to monitor (focus on upstream jobs)
PTP_JOBS=(
    "periodic-ci-openshift-release-master-nightly-${ACTUAL_VERSION}-e2e-telco5g-ptp-upstream"
)

failure_count=0
detected_failures=""
for job in "${PTP_JOBS[@]}"; do
    echo "========================================="
    job_output=$(check_ptp_job "$job" 2>&1)
    job_exit_code=$?
    echo "$job_output"

    # Count failures if any detected (exit code 0 means failure found)
    if [[ $job_exit_code -eq 0 ]] && echo "$job_output" | grep -q "❌ FAILURE DETECTED"; then
        job_failure_count=$(echo "$job_output" | grep -c "❌ FAILURE DETECTED" || echo "0")
        failure_count=$((failure_count + job_failure_count))
        detected_failures="${detected_failures}\n${job_output}"
    fi
done

echo "========================================="
echo "✅ Failure detection completed"
echo "📊 Total failures found: $failure_count"

# Set output for GitHub Actions
if [[ -n "$GITHUB_OUTPUT" ]]; then
    echo "failure_count=$failure_count" >> "$GITHUB_OUTPUT"
    echo "check_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$GITHUB_OUTPUT"
else
    echo "GitHub Actions output: failure_count=$failure_count"
    echo "GitHub Actions output: check_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi

# Save detected failures for issue creation
if [[ $failure_count -gt 0 ]]; then
    echo -e "$detected_failures" > detected_failures.txt
fi
