# Navigator

This module is reserved for bridge scheduling logic.

User manifests do not choose `spec.nodeName`. Harbor accepts unscheduled Pods,
the navigator chooses a Ready node, and the control plane writes `spec.nodeName`
as the internal assignment consumed by sailer.
