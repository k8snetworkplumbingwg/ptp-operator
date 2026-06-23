#!/bin/bash
set -x
set -euo pipefail

CA_BUNDLE="$(kubectl get secret webhook-server-cert -n openshift-ptp -o jsonpath='{.data.ca\.crt}')"
kubectl patch validatingwebhookconfiguration ptpconfig-validating-webhook-configuration --type='json' -p="[{'op': 'replace', 'path': '/webhooks/0/clientConfig/caBundle', 'value': '${CA_BUNDLE}'}]"
kubectl patch validatingwebhookconfiguration ptpconfig-validating-webhook-configuration --type='json' -p="[{'op': 'replace', 'path': '/webhooks/1/clientConfig/caBundle', 'value': '${CA_BUNDLE}'}]"
