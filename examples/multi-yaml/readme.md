# Multiple Resources in One Template

This example demonstrates how to generate multiple Kubernetes resources from a single template using CUE list syntax.

The template generates both a ClusterRole and a ClusterRoleBinding from the same template.

## How it works

1. The template queries for AWS CRDs
2. It extracts unique API groups
3. It uses CUE list syntax `[...]` to output multiple resources:
   - A ClusterRole with permissions to read resources from the API groups
   - A ClusterRoleBinding that links the ClusterRole to the default service account

This pattern is useful when you need to create related resources together.

# Extending Kubernetes RBAC
Kubernetes RBAC is powerful, but sometimes it is a bit limited. Imagine you want a `ClusterRole` that provides read access to all the Crossplane resources in your cluster that are provided by
the Crossplane AWS providers. Each provider creates resources under a multitude of API groups: `s3.aws.upbound.io`, `dynamodb.aws.upbound.io`, etc. Since Kubernetes doesn't support wildcards in the `apiGroups` field of the `ClusterRole`, you'll have to list them all manually, and keep this list up-to-date as new AWS providers are installed.

Kumquat can help! In the following `Template`, kumquat queries for all the CRDs in the cluster, and finds those that end in `.aws.upbound.io`. It passes those to a CUE program that finds the set of unique API group names, and outputs a `ClusterRole` with those API groups.

```yaml
apiVersion: kumquat.guidewire.com/v1beta1
kind: Template
metadata:
  name: generate-role
  namespace: templates
spec:
  query: | #sql
    SELECT crd.data AS crd
    FROM "CustomResourceDefinition.apiextensions.k8s.io" AS crd
    WHERE crd.name LIKE "%.aws.upbound.io"
  template:
    language: cue
    batchModeProcessing: true
    data: | #cue
      _unique_groups_map: {
        for result in DATA {
          "\(result.crd.spec.group)": result.crd.spec.group
        }
      }
      _unique_groups: [ for g in _unique_groups_map {g}]
      {
        apiVersion: "rbac.authorization.k8s.io/v1"
        kind: "ClusterRole"
        metadata: 
          name: "crossplane-aws-readers-role"
        rules: [
            {
              apiGroups: _unique_groups
              resources: "*"
              verbs: ["get", "list", "watch"]
            }
        ]
      }
```

The expected output depends on which Crossplane AWS providers are installed, but should be roughly like this:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
    name: crossplane-aws-readers-role
rules:
    - apiGroups:
        - dynamodb.aws.upbound.io
        - opensearch.aws.upbound.io
        - appautoscaling.aws.upbound.io
      resources: '*'
      verbs:
        - get
        - list
        - watch
```

Because kumquat is a controller, when new Crossplane AWS providers are installed it will automatically
detect the new CRDs and update the `ClusterRole` accordingly.