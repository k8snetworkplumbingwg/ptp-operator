# PTP Nightly Failure Detector

## Overview

The PTP Nightly Failure Detector is a GitHub Action that automatically monitors OpenShift CI (Prow) for PTP-related test failures in nightly runs on the main branch. It helps identify issues early and creates GitHub issues for investigation.

## Features

- **Automated Monitoring**: Runs daily at 8 AM EST to check for new failures
- **OpenShift Version Support**: Configurable to monitor specific OpenShift versions (default: 4.21)
- **Intelligent Filtering**: Filters out platform failures and focuses on PTP-specific issues
- **Artifact Analysis**: Downloads and analyzes test artifacts to identify root causes
- **GitHub Integration**: Automatically creates issues for detected failures
- **Manual Triggering**: Can be manually triggered with custom parameters

## How It Works

1. **Job Monitoring**: Monitors specific PTP-related Prow jobs:
   - `periodic-ci-openshift-kni-cnf-features-deploy-release-{version}-e2e-telco5g-ptp`
   - `periodic-ci-openshift-kni-cnf-features-deploy-release-{version}-e2e-telco5g-ptp-operator`
   - `e2e-telco5g-ptp`
   - `cnf-e2e-ptp`

2. **Failure Detection**: Checks for jobs that failed in the specified time window

3. **Artifact Analysis**: Downloads and parses test artifacts looking for:
   - PTP-specific errors (ptp4l, phc2sys, clock synchronization)
   - Test failures and timeouts
   - Hardware/driver issues

4. **Issue Management**: Creates GitHub issues with detailed failure reports

## Configuration

### Environment Variables

- `OPENSHIFT_VERSION`: OpenShift version to monitor (default: "main" for latest, or specific version like "4.21")
- `LOOKBACK_HOURS`: Hours to look back for failures (default: "24")

### Schedule

The workflow runs automatically:
- Daily at 8 AM EST: `0 13 * * *` (1 PM UTC)
- Only on the main branch

### Manual Trigger

You can manually trigger the workflow with custom parameters:

1. Go to Actions tab in GitHub
2. Select "PTP Nightly Failure Detector"
3. Click "Run workflow"
4. Optionally specify:
   - OpenShift version (e.g., "main", "4.21", "4.22")
   - Lookback hours (e.g., "12", "48")

## Failure Analysis

### What Gets Detected

✅ **PTP-Specific Failures**:
- ptp4l process failures
- phc2sys synchronization issues
- Clock drift problems
- PTP configuration errors
- Hardware compatibility issues

❌ **Filtered Out**:
- Platform infrastructure failures
- Network connectivity issues
- Generic Kubernetes failures
- Test environment setup problems

### Example Failure Report

```markdown
# 🚨 PTP Nightly Test Failures Detected

**Detection Time:** 2024-03-15T08:30:00Z
**OpenShift Version:** main
**Failures Found:** 2
**Lookback Period:** 24 hours

## 📋 Summary

Automated failure detection found 2 PTP-related test failures...

## 🚨 Detected Failures

```
❌ FAILURE DETECTED:
   Job: periodic-ci-openshift-release-master-nightly-4.21-e2e-telco5g-ptp-upstream
   Time: 2024-03-15T06:30:00Z
   State: failure
   URL: https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/...
```

## 🔍 Investigation Required

Please review the job failures and artifacts to identify:
- PTP configuration issues
- Hardware/driver problems
- Test environment issues
- Code regressions

## 🤖 AI Analysis Available

To get AI-powered analysis of these failures, comment `@ai-triage` on this issue.
```

## GitHub Issue Management

### Labels Applied

- `bug`: Indicates this is a bug report
- `ptp`: PTP-related issue
- `nightly-failure`: Detected by automated monitoring
- `needs-investigation`: Requires human investigation

### Issue Lifecycle

1. **Creation**: New issue created when failures are first detected
2. **Updates**: Existing issues are updated if more failures occur on the same day
3. **Manual Closure**: Issues should be manually closed once investigated and resolved

## Troubleshooting

### Common Issues

1. **No Artifacts Found**
   - Prow job may not have completed artifact upload
   - Artifacts may be in a different location
   - Network issues accessing Prow

2. **False Positives**
   - Platform failures incorrectly classified as PTP failures
   - Transient network issues
   - Test environment problems

3. **Missing Failures**
   - Job names may have changed
   - Different artifact structure
   - API access issues

### Debugging

Check the GitHub Action logs for:
- HTTP response codes from Prow API
- Artifact download status
- Parsing errors

## Customization

### Adding New Jobs

To monitor additional PTP jobs, modify the `PTP_JOBS` array in the workflow:

```bash
PTP_JOBS=(
    "periodic-ci-openshift-kni-cnf-features-deploy-release-${OPENSHIFT_VERSION}-e2e-telco5g-ptp"
    "periodic-ci-openshift-kni-cnf-features-deploy-release-${OPENSHIFT_VERSION}-e2e-telco5g-ptp-operator"
    "e2e-telco5g-ptp"
    "cnf-e2e-ptp"
    "your-new-ptp-job-name"
)
```

### Modifying Failure Patterns

Update the `analyze_artifact_content` function to detect additional failure patterns:

```bash
# Look for new error patterns
if echo "$content" | grep -q "your-new-error-pattern"; then
    echo "   🚨 Custom failure detected"
fi
```

## Security Considerations

- Uses `GITHUB_TOKEN` for creating issues (read-only repository access)
- Only accesses public Prow APIs
- No sensitive data is exposed in logs or artifacts

## Dependencies

- `curl`: For HTTP requests to Prow APIs
- `jq`: For JSON parsing
- `gh`: GitHub CLI for issue management
- Standard Unix tools: `grep`, `sed`, `date`

## Related Documentation

- [Prow Dashboard](https://prow.ci.openshift.org/)
- [OpenShift PTP Documentation](https://docs.openshift.com/container-platform/latest/networking/using-ptp.html)
- [GitHub Actions Documentation](https://docs.github.com/en/actions)