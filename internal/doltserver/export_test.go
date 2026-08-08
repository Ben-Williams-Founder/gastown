package doltserver

// Test-only bridge so external-package tests (package doltserver_test) can
// reach unexported helpers. External test packages are required for any test
// file importing internal/testutil: testutil imports doltserver, so an
// in-package test importing testutil forms a test-only import cycle under the
// nightly -tags=integration matrix.
var (
	DoltSQLScriptForTest     = doltSQLScript
	IsNothingToCommitForTest = isNothingToCommit
	// Defined in wl_commons_conformance_test.go (same in-package test archive).
	WLCommonsConformanceForTest = wlCommonsConformance
	BdSQLForTest                = bdSQL
	BdSQLCountForTest           = bdSQLCount
	BdTableExistsForTest        = bdTableExists
)
