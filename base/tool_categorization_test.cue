// Package base — CUE constraint tests for tool_categorization.cue.
// Run via: cue vet ./base/...
//
// Each `_test_*` field unifies a literal against the schema. Invalid literals
// (e.g. `#ToolType & "saas"`) would fail `cue vet`. We assert the curated set
// shape here so renames or accidental deletions surface immediately.
package base

_test_tool_type_oss:     #ToolType & "oss"
_test_tool_type_managed: #ToolType & "managed"
_test_tool_type_hybrid:  #ToolType & "hybrid"

_test_tool_category_database:     #ToolCategory & "database"
_test_tool_category_auth:         #ToolCategory & "auth"
_test_tool_category_ingress:      #ToolCategory & "ingress"
_test_tool_category_ai_workload:  #ToolCategory & "ai-workload"
_test_tool_category_dev_platform: #ToolCategory & "dev-platform"
