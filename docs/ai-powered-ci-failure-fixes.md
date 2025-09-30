# AI-Powered CI Failure Detection and Automated Fixes

## Overview

This document outlines the implementation plan for an AI-powered system that automatically detects, analyzes, and proposes fixes for CI failures in the PTP Operator project, inspired by Red Hat's CVE automation approach but adapted for CI/CD pipeline failures.

## Current State

We have an **PTP Nightly Failure Detector** GitHub Action that:
- **Workflow File**: `.github/workflows/ptp-nightly-failure-detector.yaml` (ready for deployment)
- **Functionality**: Runs every 6 hours to detect PTP test failures
- **Issue Creation**: Automatically creates GitHub issues when failures are detected
- **Analysis**: Provides detailed failure analysis with artifact inspection
- **Integration Ready**: Includes AI analysis trigger support (`@ai-triage` comments)

## Proposed Enhancement: AI-Powered Failure Resolution

### Core Architecture

```
GitHub Actions (Agent) ←→ Gemini/Claude CLI (AI Analysis) ←→ GitHub MCP Server (Repository Actions)
```

### Key Components

#### 1. **GitHub Actions (Agent)**
- Extends existing failure detector workflow
- Triggers AI analysis when failures are detected
- Orchestrates the fix proposal and review process
- Manages branch creation and PR submission

#### 2. **Gemini CLI with ReAct Loop (`run-gemini-cli`)**
- **GitHub Actions Integration**: The `run-gemini-cli` action integrates Gemini CLI into development workflow
- **Autonomous Agent**: Acts as an autonomous agent for performing comprehensive code analysis
- **ReAct (Reason and Act) Loop**: Uses reasoning and action cycles with built-in tools and MCP servers
- **Complex Use Case Handling**: Specialized for reading code, analyzing dependencies, and fixing bugs
- **Gemini API Integration**: Leverages Gemini API's advanced capabilities for intelligent analysis
- **Cross-Repository Analysis**: Deep failure analysis across all three PTP repositories
- **Context-Aware**: Understanding of PTP ecosystem architecture and interdependencies

#### 3. **GitHub MCP Server**
- Provides AI agent access to repository operations
- Enables reading files, creating branches, and updating code
- Manages issue comments and PR creation
- Controlled access to prevent unauthorized changes

## Implementation Workflow

### Stage 1: Enhanced Failure Detection
**Trigger**: Issue creation in repository (automatically or manually created)

**PTP Ginkgo Test Analysis Context**:
1. **Primary Focus**: PTP Operator Ginkgo test failures from `e2e-telco5g-ptp-upstream` job
2. **Repository**: `k8snetworkplumbingwg/ptp-operator` (main focus for test failures)
3. **Supporting Repositories** (for context when needed):
   - `k8snetworkplumbingwg/linuxptp-daemon` (underlying PTP implementation)
   - `redhat-cne/cloud-event-proxy` (event handling integration)

4. **Ginkgo Test Failure Analysis**:
   - **Prow Job Monitoring**: Focus on `e2e-telco5g-ptp-upstream` job failures only
   - **Artifact Deep Dive**: Parse JUnit XML and test logs from specific artifact paths
   - **PTP Test Classification**: Distinguish PTP test failures from platform/infrastructure issues
   - **Ginkgo Output Parsing**: Extract specific test case failures and error messages
   - **Historical Pattern Recognition**: Identify recurring PTP test failure patterns

### Stage 2: Automated Triage (`@ai-triage`)
**Trigger**: Comment `@ai-triage` on failure issue

**Process**:
```yaml
- name: AI PTP Ginkgo Test Analysis
  prompt: |-
    You are a PTP test engineer analyzing Ginkgo test failures from the ptp-operator repository.

    Test Context:
    - Repository: k8snetworkplumbingwg/ptp-operator
    - Test Framework: Ginkgo tests for PTP functionality
    - Target Job: e2e-telco5g-ptp-upstream
    - Prow URL Pattern: https://prow.ci.openshift.org/?job=**e2e-telco5g-ptp-upstream**

    Failure Analysis Focus:
    - ONLY analyze jobs with state: "failure"
    - IGNORE all platform failures and infrastructure issues
    - Focus on PTP-specific test failures in Ginkgo test suite

    Artifacts Location Pattern:
    - Job URL: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-release-master-nightly-4.21-e2e-telco5g-ptp-upstream/{JOB_ID}
    - Artifacts: https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/logs/periodic-ci-openshift-release-master-nightly-4.21-e2e-telco5g-ptp-upstream/{JOB_ID}/artifacts/e2e-telco5g-ptp-upstream/telco5g-ptp-tests/artifacts/

    TASK: Analyze PTP test artifacts and identify Ginkgo test failures.

    Required Analysis Steps:
    1. **Artifact Inspection**: Examine JUnit XML and test logs from artifacts directory
    2. **Ginkgo Test Parsing**: Parse Ginkgo test output for specific PTP test failures
    3. **PTP Error Classification**: Identify PTP-specific vs platform issues

    Analysis must include:
    1. **Test Failure Summary** - Which specific Ginkgo tests failed?
    2. **PTP Root Cause** - What PTP functionality is broken (ptp4l, phc2sys, sync, config)?
    3. **Failure Classification** - Is this a test case issue or actual PTP operator bug?
    4. **Proposed Fix Location** - Fix needed in ptp-operator repository:
       - Test case fix: Update/fix the failing Ginkgo test
       - Operator bug fix: Fix actual PTP operator functionality
    5. **Test Reproduction** - How to reproduce and verify the fix
```

### Stage 3: Automated Fix Creation (`@ai-create-fix`)
**Trigger**: Comment `@ai-create-fix` after triage approval

**Process**:
```yaml
- name: AI PTP Operator Fix Implementation
  prompt: |-
    You are implementing a fix for PTP Ginkgo test failures in the ptp-operator repository.

    Repository Context:
    - Focus: k8snetworkplumbingwg/ptp-operator
    - Test Framework: Ginkgo test suite
    - Failed Job: e2e-telco5g-ptp-upstream

    TASK: Create fix branch for issue #${{ env.ISSUE_NUMBER }}

    STEP 1 - PARSE TRIAGE: Extract PTP test failure classification from triage analysis
    STEP 2 - CREATE BRANCH: Branch name: ptp-fix-issue-${{ env.ISSUE_NUMBER }}-${{ github.run_number }}
    STEP 3 - APPLY FIX: Choose appropriate fix in ptp-operator repository:
       - If test case issue: Fix/update the failing Ginkgo test cases
       - If operator bug: Fix the actual PTP operator functionality/code
    STEP 4 - VALIDATE: Ensure changes follow PTP operator patterns and test best practices
    STEP 5 - REPORT: Comment with fix summary and e2e-telco5g-ptp-upstream validation steps
```

## MCP Tools Usage by Stage

### Analysis Stage (PTP Ginkgo Test Focus)
- `get_issue` - Read PTP test failure issue details
- `add_issue_comment` - Post Ginkgo test analysis results
- `get_file_contents` - Examine PTP operator source files and test files
- `search_code` - Find related code patterns in ptp-operator repository
- `web_fetch` - Retrieve artifacts from Prow/GCS (JUnit XML, test logs)
- `list_issues` - Check for related PTP test failure issues

### Fix Creation Stage (PTP Operator Focus)
- `create_branch` - Create fix branch in ptp-operator repository
- `create_or_update_file` - Apply code/test changes in ptp-operator
- `search_code` - Validate fix completeness in PTP operator codebase
- `add_issue_comment` - Report fix completion with Ginkgo test validation
- `create_pull_request` - Submit PR to ptp-operator repository
- `get_file_contents` - Reference supporting repos for context when needed

## Implementation Plan

### Phase 1: Foundation (Week 1-2)
- [ ] Set up AI CLI integration in GitHub Actions (triggers on issue creation)
- [ ] Configure GitHub MCP server access to all three repositories:
  - `k8snetworkplumbingwg/ptp-operator`
  - `k8snetworkplumbingwg/linuxptp-daemon`
  - `redhat-cne/cloud-event-proxy`
- [ ] Create cross-repository analysis prompts with interdependency context
- [ ] Test with historical failure data across all three repos

### Phase 2: Core Features (Week 3-4)
- [ ] Implement automated triage workflow
- [ ] Develop fix generation capabilities
- [ ] Create approval gates and safety checks
- [ ] Add comprehensive logging and monitoring

### Phase 3: Enhancement (Week 5-6)
- [ ] Historical failure pattern learning
- [ ] Multi-fix proposal capability
- [ ] Integration with existing review processes
- [ ] Performance optimization and error handling

### Phase 4: Production (Week 7-8)
- [ ] Team training and documentation
- [ ] Gradual rollout with manual oversight
- [ ] Feedback collection and refinement
- [ ] Full automation with safety controls

## Safety and Review Process

### Automated Safeguards
1. **Dry Run Mode**: AI proposes fixes without applying them
2. **Code Review Gates**: All AI fixes require human approval
3. **Test Validation**: Fixes must pass existing test suites
4. **Rollback Capability**: Easy reversion of AI-generated changes

### Human Oversight Points
1. **Triage Approval**: Human review before fix generation
2. **Code Review**: Standard PR review process for all changes
3. **Testing Validation**: Manual testing of critical fixes
4. **Emergency Override**: Ability to disable AI system

## Success Metrics

### Efficiency Gains
- **Time to Detection**: Reduce from hours to minutes
- **Analysis Time**: Reduce from 2-3 hours to 15-30 minutes
- **Fix Development**: Reduce from days to hours
- **Overall Resolution**: Target 50% reduction in failure resolution time

### Quality Metrics
- **Fix Success Rate**: Target 80% of AI fixes resolve the issue
- **False Positive Rate**: Keep under 10%
- **Regression Prevention**: No new issues introduced by AI fixes

## Repository-Specific Context

### PTP Operator Failure Patterns
```yaml
context_prompts:
  timing_issues: "PTP synchronization often fails due to timing precision requirements"
  hardware_deps: "Tests may fail on virtualized environments lacking PTP hardware"
  config_errors: "Common misconfigurations in PTP4L and PHC2SYS settings"
  race_conditions: "Multi-pod PTP configurations can have startup race conditions"
```

### Common Fix Categories
1. **Timeout Adjustments**: Increase wait times for PTP sync
2. **Configuration Updates**: Fix PTP daemon configurations
3. **Test Environment**: Add hardware requirement checks
4. **Error Handling**: Improve error detection and recovery

## Security and Secret Management

### Protecting API Keys in Upstream Repository

When running on upstream repositories, protecting `GEMINI_API_KEY` is critical:

#### **Option 1: Organization-Level Secrets (Recommended)**
```yaml
# Repository Settings > Secrets and variables > Actions
# Set as Organization secret with repository access control
secrets.GEMINI_API_KEY  # Available only to authorized repositories
```

#### **Option 2: Environment-Based Protection**
```yaml
jobs:
  ai-analysis:
    environment: ai-production  # Requires approval for sensitive operations
    if: |
      github.repository_owner == 'k8snetworkplumbingwg' &&
      (github.event_name == 'issues' || github.event_name == 'issue_comment')
```

#### **Option 3: Fork-Safe Configuration**
```yaml
- name: Check for API Key
  id: check-key
  run: |
    if [[ -z "${{ secrets.GEMINI_API_KEY }}" ]]; then
      echo "api-available=false" >> $GITHUB_OUTPUT
      echo "⚠️ Gemini API key not available - skipping AI analysis"
    else
      echo "api-available=true" >> $GITHUB_OUTPUT
    fi

- name: Run Gemini CLI (Only if API key available)
  if: steps.check-key.outputs.api-available == 'true'
  uses: ./.github/actions/run-gemini-cli
```

#### **Option 4: External Service Integration**
```yaml
# Use a separate service/webhook for AI processing
- name: Trigger External AI Service
  run: |
    curl -X POST "${{ secrets.AI_SERVICE_WEBHOOK_URL }}" \
      -H "Authorization: Bearer ${{ secrets.AI_SERVICE_TOKEN }}" \
      -d '{
        "repository": "${{ github.repository }}",
        "issue": "${{ github.event.issue.number }}",
        "action": "${{ github.event.action }}"
      }'
```

### Additional Security Measures

#### **Workflow Security Controls**
```yaml
permissions:
  contents: read          # Minimal read access
  issues: write          # Only for commenting on issues
  pull-requests: write   # Only for creating PRs
  # No secrets, packages, or actions permissions

concurrency:
  group: ai-analysis-${{ github.event.issue.number }}
  cancel-in-progress: true  # Prevent multiple runs
```

#### **Repository Protection Rules**
- **Branch Protection**: Require reviews for AI-generated PRs
- **Fork Restrictions**: Limit workflow execution on forks
- **Approval Gates**: Require maintainer approval for sensitive operations

## Risk Mitigation

### Security Risks
- **API Key Exposure**: Use organization secrets with access controls
- **Fork Attacks**: Implement fork-safe workflows with key availability checks
- **Unauthorized Access**: Restrict workflow triggers to repository owners only
- **Secret Leakage**: Never log or expose API keys in workflow outputs

### Technical Risks
- **AI Hallucination**: Multiple validation layers and human review
- **Code Quality**: Enforce coding standards and test coverage
- **Limited Scope**: AI changes restricted to specific file patterns

### Process Risks
- **Over-automation**: Maintain human oversight and control
- **Team Skills**: Ensure team understands AI-generated fixes
- **Dependency Risk**: Have manual fallback procedures

## Future Enhancements

### Advanced Features
- **Predictive Failure Detection**: Identify issues before they cause failures
- **Cross-Repository Learning**: Share patterns across related projects
- **Performance Optimization**: AI-driven performance improvements
- **Documentation Generation**: Auto-update docs based on fixes

### Integration Opportunities
- **Slack/Teams Integration**: Real-time notifications and approvals
- **Jira Integration**: Automatic ticket creation and updates
- **Monitoring Integration**: Proactive failure prevention
- **Release Pipeline**: Integration with automated releases

## Getting Started

### Prerequisites
1. **Gemini CLI** - AI inference engine with ReAct (Reason and Act) loop capabilities
2. **GitHub MCP server** - Model Context Protocol server for repository operations
3. **Multi-repository access permissions**:
   - `k8snetworkplumbingwg/ptp-operator` (read/write)
   - `k8snetworkplumbingwg/linuxptp-daemon` (read/write)
   - `redhat-cne/cloud-event-proxy` (read/write)
4. **Team training** on AI workflow and cross-repository dependencies

### Initial Setup
```bash
# 1. Install Gemini CLI with ReAct capabilities
pip install gemini-cli

# 2. Configure GitHub MCP with multi-repo access
npm install @modelcontextprotocol/server-github

# 3. Setup GitHub Actions secrets for cross-repository access
# - GEMINI_API_KEY (Gemini API access)
# - GITHUB_TOKEN (with repo access to all three repositories)
# - PTP_OPERATOR_TOKEN (if separate token needed)
# - LINUXPTP_DAEMON_TOKEN (if separate token needed)
# - CLOUD_EVENT_PROXY_TOKEN (if separate token needed)

# 4. Deploy enhanced workflow with issue creation trigger
cp .github/workflows/ai-failure-detector.yml .github/workflows/

# 5. Configure cross-repository webhooks for issue creation triggers
```

### Secure Workflow Configuration
```yaml
name: AI-Powered PTP Failure Analysis
on:
  issues:
    types: [opened, labeled]
  issue_comment:
    types: [created]
  workflow_dispatch:

# Security: Minimal permissions
permissions:
  contents: read
  issues: write
  pull-requests: write

env:
  PTP_OPERATOR_REPO: "k8snetworkplumbingwg/ptp-operator"
  LINUXPTP_DAEMON_REPO: "k8snetworkplumbingwg/linuxptp-daemon"
  CLOUD_EVENT_PROXY_REPO: "redhat-cne/cloud-event-proxy"

# Security: Prevent concurrent runs per issue
concurrency:
  group: ai-analysis-${{ github.event.issue.number }}
  cancel-in-progress: true

jobs:
  ai-analysis:
    # Security: Only run on upstream repository
    if: |
      github.repository_owner == 'k8snetworkplumbingwg' &&
      (
        (github.event.action == 'opened' && contains(github.event.issue.title, 'PTP')) ||
        (github.event.action == 'created' && contains(github.event.comment.body, '@ai-triage')) ||
        (github.event.action == 'created' && contains(github.event.comment.body, '@ai-create-fix'))
      )
    runs-on: ubuntu-latest
    environment: ai-production  # Requires approval for production AI operations

    steps:
      - name: Checkout repository
        uses: actions/checkout@v4

      - name: Check API Key Availability
        id: check-key
        run: |
          if [[ -z "${{ secrets.GEMINI_API_KEY }}" ]]; then
            echo "api-available=false" >> $GITHUB_OUTPUT
            echo "⚠️ Gemini API key not available - AI analysis will be skipped"
            echo "This is expected on forks. For upstream maintainers, please configure organization secrets."
          else
            echo "api-available=true" >> $GITHUB_OUTPUT
            echo "✅ Gemini API key available - proceeding with AI analysis"
          fi

      - name: Run Gemini CLI Autonomous Agent
        if: steps.check-key.outputs.api-available == 'true'
        uses: ./.github/actions/run-gemini-cli
        with:
          api-key: ${{ secrets.GEMINI_API_KEY }}
          repositories: "$PTP_OPERATOR_REPO,$LINUXPTP_DAEMON_REPO,$CLOUD_EVENT_PROXY_REPO"
          issue-number: ${{ github.event.issue.number }}
          trigger-type: ${{ github.event.action }}
          mcp-server: "github"
          github-token: ${{ secrets.GITHUB_TOKEN }}

      - name: Fallback for Contributors
        if: steps.check-key.outputs.api-available == 'false'
        uses: actions/github-script@v7
        with:
          script: |
            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: `🤖 **AI Analysis Not Available**

              AI-powered failure analysis is only available on the upstream repository with proper API key configuration.

              **For maintainers**: Please ensure \`GEMINI_API_KEY\` is configured as an organization secret.
              **For contributors**: A maintainer will need to manually trigger AI analysis or review your issue.

              You can still use the existing [PTP Nightly Failure Detector](https://github.com/k8snetworkplumbingwg/ptp-operator-k8/actions/workflows/ptp-nightly-failure-detector.yaml) for basic failure detection.`
            });
```

### Gemini CLI Action Architecture

The `run-gemini-cli` GitHub Action provides the core intelligence for the AI-powered failure analysis:

```yaml
# .github/actions/run-gemini-cli/action.yml
name: 'Run Gemini CLI Autonomous Agent'
description: 'Integrates Gemini CLI into development workflow for code analysis and bug fixing'

inputs:
  api-key:
    description: 'Gemini API key for LLM access'
    required: true
  repositories:
    description: 'Comma-separated list of repositories to analyze'
    required: true
  issue-number:
    description: 'GitHub issue number to analyze'
    required: true
  trigger-type:
    description: 'Type of trigger (opened, created, etc.)'
    required: true
  mcp-server:
    description: 'MCP server type (github)'
    required: true
    default: 'github'
  github-token:
    description: 'GitHub token for repository access'
    required: true

runs:
  using: 'composite'
  steps:
    - name: Setup Gemini CLI
      run: |
        pip install gemini-cli
        gemini-cli configure --api-key ${{ inputs.api-key }}
      shell: bash

    - name: Execute ReAct Loop Analysis
      run: |
        gemini-cli react-loop \
          --task "analyze-ci-failure" \
          --repos ${{ inputs.repositories }} \
          --issue ${{ inputs.issue-number }} \
          --trigger ${{ inputs.trigger-type }} \
          --mcp-server ${{ inputs.mcp-server }} \
          --github-token ${{ inputs.github-token }}
      shell: bash
```

### Team Training
1. **AI Workflow Overview**: Understanding the automated process
2. **Review Process**: How to evaluate AI-generated fixes
3. **Emergency Procedures**: Disabling AI when needed
4. **Feedback Loop**: Improving AI performance over time

---

**Next Steps**:
1. Team review and approval of this implementation plan
2. Setup development environment for testing
3. Create pilot implementation with limited scope
4. Gradual rollout with extensive monitoring

**Expected Timeline**: 8 weeks from approval to production deployment
**Resource Requirements**: 1-2 engineers, AI API access, additional GitHub Actions minutes