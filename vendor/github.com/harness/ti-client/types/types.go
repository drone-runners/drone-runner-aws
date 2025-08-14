package types

import "github.com/harness/ti-client/types/cache/dlc"

type Status string
type FileStatus string
type Selection string

const (
	// StatusPassed represents a passed test.
	StatusPassed = "passed"

	// StatusSkipped represents a test case that was intentionally skipped.
	StatusSkipped = "skipped"

	// StatusFailure represents a violation of declared test expectations,
	// such as a failed assertion.
	StatusFailed = "failed"

	// StatusError represents an unexpected violation of the test itself, such as
	// an uncaught exception.
	StatusError = "error"

	// SelectSourceCode represents a selection corresponding to source code changes.
	SelectSourceCode = "source_code"

	// SelectNewTest represents a selection corresponding to a new test (eg a new test
	// introduced in the PR).
	SelectNewTest = "new_test"

	// SelectUpdatedTest represents a selection corresponding to an updated test (eg an existing
	// test which was modified).
	SelectUpdatedTest = "updated_test"

	// SelectFlakyTest represents a selection of a test because it's flaky.
	SelectFlakyTest = "flaky_test"

	// SelectAlwaysRunTest represents a selection of a test because it always must be run.
	SelectAlwaysRunTest = "always_run_test"

	// FileModified represents a modified file. Keeping it consistent with git syntax.
	FileModified = "modified"

	// FileAdded represents a file which was added in the PR.
	FileAdded = "added"

	// FileDeleted represents a file which was deleted in the PR.
	FileDeleted = "deleted"

	// AccountIDEnv represents the environment variable for Harness Account ID of the pipeline execution
	AccountIDEnv = "HARNESS_ACCOUNT_ID"

	// OrgIDEnv represents the environment variable for Organization ID of the pipeline execution
	OrgIDEnv = "HARNESS_ORG_ID"

	// ProjectIDEnv represents the environment variable for Project ID of the pipeline execution
	ProjectIDEnv = "HARNESS_PROJECT_ID"

	// PipelineIDEnv represents the environment variable for Pipeline ID of the pipeline execution
	PipelineIDEnv = "HARNESS_PIPELINE_ID"

	// StageIDEnv represents the environment variable for Stage ID of the stage
	StageIDEnv = "HARNESS_STAGE_ID"

	// StepIDEnv represents the environment variable for Step ID of the step
	StepIDEnv = "HARNESS_STEP_ID"

	// BuildIDEnv represents the environment variable for Build ID of the pipeline execution
	BuildIDEnv = "HARNESS_BUILD_ID"

	// TiSvcEp represents the environment variable for TI service endpoint
	TiSvcEp = "HARNESS_TI_SERVICE_ENDPOINT"

	// TiSvcToken represents the environment variable for TI service token
	TiSvcToken = "HARNESS_TI_SERVICE_TOKEN" //nolint:gosec

	// InfraEnv represents the environment variable for infra on which the pipeline is running
	InfraEnv = "HARNESS_INFRA"

	// HarnessInfra represents the environment in which the build is running
	HarnessInfra = "VM"
)

func ConvertToFileStatus(s string) FileStatus {
	switch s {
	case FileModified:
		return FileModified
	case FileAdded:
		return FileAdded
	case FileDeleted:
		return FileDeleted
	}
	return FileModified
}

type Result struct {
	Status  Status `json:"status"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Desc    string `json:"desc"`
}

type ResponseMetadata struct {
	TotalPages    int `json:"totalPages"`
	TotalItems    int `json:"totalItems"`
	PageItemCount int `json:"pageItemCount"`
	PageSize      int `json:"pageSize"`
}

type TestCases struct {
	Metadata ResponseMetadata `json:"data"`
	Tests    []TestCase       `json:"content"`
}

type TestSuites struct {
	Metadata ResponseMetadata `json:"data"`
	Suites   []TestSuite      `json:"content"`
}

type TestCase struct {
	Name       string `json:"name"`
	ClassName  string `json:"class_name"`
	FileName   string `json:"file_name"`
	SuiteName  string `json:"suite_name"`
	Result     Result `json:"result"`
	DurationMs int64  `json:"duration_ms"`
	SystemOut  string `json:"stdout"`
	SystemErr  string `json:"stderr"`
}

type TestSummary struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
}

type SummaryRequest struct {
	AllStages  bool
	OrgID      string
	ProjectID  string
	PipelineID string
	BuildID    string
	StageID    string
	StepID     string
	ReportType string
}

type TestCasesRequest struct {
	BasicInfo          SummaryRequest
	TestCaseSearchTerm string
	Sort               string
	Order              string
	PageIndex          string
	PageSize           string
	SuiteName          string
}

type SummaryResponse struct {
	TotalTests      int   `json:"total_tests"`
	FailedTests     int   `json:"failed_tests"`
	SuccessfulTests int   `json:"successful_tests"`
	SkippedTests    int   `json:"skipped_tests"`
	TimeMs          int64 `json:"duration_ms"`
}

type StepInfo struct {
	Step  string `json:"step"`
	Stage string `json:"stage"`
}

type TestSuite struct {
	Name         string `json:"name"`
	DurationMs   int64  `json:"duration_ms"`
	TotalTests   int    `json:"total_tests"`
	FailedTests  int    `json:"failed_tests"`
	SkippedTests int    `json:"skipped_tests"`
	PassedTests  int    `json:"passed_tests"`
	FailPct      int    `json:"fail_pct"`
}

// Test Intelligence specific structs

// RunnableTest contains information about a test to run it.
// This is different from TestCase struct which contains information
// about a test case run. RunnableTest is used to run a test.
type RunnableTest struct {
	Pkg        string    `json:"pkg"`
	Class      string    `json:"class"`
	Method     string    `json:"method"`
	Selection  Selection `json:"selection"` // information on why a test was selected
	Autodetect struct {
		// auto-detection info depending on the runner
		Rule string `json:"rule"` // bazel
	} `json:"autodetect"`
}

type SelectTestsResp struct {
	TotalTests    int            `json:"total_tests"`
	SelectedTests int            `json:"selected_tests"`
	NewTests      int            `json:"new_tests"`
	UpdatedTests  int            `json:"updated_tests"`
	SrcCodeTests  int            `json:"src_code_tests"`
	SelectAll     bool           `json:"select_all"` // We might choose to run all the tests
	Tests         []RunnableTest `json:"tests"`
}

type SelectTestsReq struct {
	// If this is specified, TI service will return saying it wants to run all the tests. We want to
	// maintain stats even when all the tests are run.
	SelectAll    bool     `json:"select_all"`
	Files        []File   `json:"files"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Repo         string   `json:"repo"`
	TiConfig     TiConfig `json:"ti_config"`
	TestGlobs    []string `json:"test_globs"`
	Language     string   `json:"language"`
}

type SelectionDetails struct {
	New int `json:"new_tests"`
	Upd int `json:"updated_tests"`
	Src int `json:"source_code_changes"`
}

type SelectionOverview struct {
	Total        int              `json:"total_tests"`
	Skipped      int              `json:"skipped_tests"`
	TimeSavedMs  int              `json:"time_saved_ms"`
	TimeTakenMs  int              `json:"time_taken_ms"`
	Repo         string           `json:"repo"`
	SourceBranch string           `json:"source_branch"`
	TargetBranch string           `json:"target_branch"`
	Selected     SelectionDetails `json:"selected_tests"`
}

type GetTestTimesReq struct {
	IncludeFilename  bool `json:"include_filename"`
	IncludeTestSuite bool `json:"include_test_suite"`
	IncludeTestCase  bool `json:"include_test_case"`
	IncludeClassname bool `json:"include_classname"`
}

type GetTestTimesResp struct {
	FileTimeMap  map[string]int `json:"file_time_map"`
	SuiteTimeMap map[string]int `json:"suite_time_map"`
	TestTimeMap  map[string]int `json:"test_time_map"`
	ClassTimeMap map[string]int `json:"class_time_map"`
}

type File struct {
	Name    string     `json:"name"`
	Status  FileStatus `json:"status"`
	Package string     `json:"package"`
}

type DownloadLink struct {
	URL     string `json:"url"`
	RelPath string `json:"rel_path"` // this is the relative path to the artifact from the base URL
}

// This is a yaml file which may or may not exist in the root of the source code
// as .ticonfig. The contents of the file get deserialized into this object.
// Sample YAML:
// config:
//
//	ignore:
//	   - README.md
//	   - config.sh
//	enableBazelOptimization: true
//	bazelFileCountThreshold: 100
type TiConfig struct {
	Config struct {
		Ignore                  []string `json:"ignore"`
		BazelOptimization       bool     `yaml:"enableBazelOptimization"`
		BazelFileCountThreshold int      `yaml:"bazelFileCountThreshold"`
	}
}

type DiffInfo struct {
	Sha   string
	Files []File
}

type MergePartialCgRequest struct {
	AccountId    string
	Repo         string
	TargetBranch string
	Diff         DiffInfo
}

// Visualization structures

// Simplified node
type VisNode struct {
	Id int `json:"id"`

	Package string `json:"package"`
	Class   string `json:"class"`
	File    string `json:"file"`
	Type    string `json:"type"`
	Root    bool   `json:"root,omitempty"`
	// Gives information about useful nodes which might be used by UI on which nodes to center
	Important bool `json:"important"`
}

type VisMapping struct {
	From int   `json:"from"`
	To   []int `json:"to"`
}

type GetVgReq struct {
	AccountId    string
	Repo         string
	SourceBranch string
	TargetBranch string
	Limit        int64
	Class        string
	DiffFiles    []File
	Language     string
}

type GetVgResp struct {
	Nodes []VisNode    `json:"nodes"`
	Edges []VisMapping `json:"edges"`
}

type GetCgCountReq struct {
	Repo                  string `json:"repo"`
	Branch                string `json:"branch"`
	SearchPushCollections bool   `json:"search_push_collections"`
}

type GetCgCountResp struct {
	NodeCount     int `json:"node_count"`
	RelationCount int `json:"relation_count"`
}

type CommitInfoResp struct {
	LastSuccessfulCommitId string `json:"commit_id"`
}

// ML Based Test Selection Request and Response
type MLSelectTestsRequest struct {
	SelectAll           bool                `json:"select_all"`
	MLServiceAPIRequest MLServiceAPIRequest `json:"ml_service_api_request"`
	Percentile          int                 `json:"percentile"`
	Files               []File              `json:"files"`
	Specs               map[string]string   `json:"specs"`
	TestRunner          string              `json:"test_runner"`
}

type MLServiceAPIRequest struct {
	ProjectDir   string   `json:"project_dir"`
	RunID        string   `json:"run_id"`
	UseCached    bool     `json:"use_cached"`
	ChangedFiles []string `json:"changed_files"`
	PRID         int      `json:"pr_id"`
	ClassName    []string `json:"class_name"`
	MethodName   []string `json:"method_name"`
	TimeCreated  string   `json:"time_created"`
	PRCommits    int      `json:"pr_commits"`
	PRAdditions  int      `json:"pr_additions"`
	PRDeletions  int      `json:"pr_deletions"`
	Authors      string   `json:"authors"`
}

type (
	TelemetryData struct {
		BuildIntelligenceMetaData BuildIntelligenceMetaData `json:"build_intelligence_data,omitempty"`
		TestIntelligenceMetaData  TestIntelligenceMetaData  `json:"test_intelligence_data,omitempty"`
		CacheIntelligenceMetaData CacheIntelligenceMetaData `json:"cache_intelligence_data,omitempty"`
		DlcMetadata               dlc.Metadata              `json:"dlc_metadata,omitempty"`
		BuildInfo                 BuildInfo                 `json:"build_info,omitempty"`
		Errors                    []string                  `json:"errors,omitempty"`
	}

	BuildIntelligenceMetaData struct {
		BuildTasks     int      `json:"build_tasks,omitempty"`
		TasksRestored  int      `json:"tasks_restored,omitempty"`
		StepType       string   `json:"step_type,omitempty"`
		IsMavenBIUsed  bool     `json:"is_maven_bi_used,omitempty"`
		IsGradleBIUsed bool     `json:"is_gradle_bi_used,omitempty"`
		IsBazelBIUsed  bool     `json:"is_bazel_bi_used,omitempty"`
		Errors         []string `json:"errors,omitempty"`
	}

	TestIntelligenceMetaData struct {
		TotalTests             int      `json:"total_tests,omitempty"`
		TotalTestClasses       int      `json:"total_test_classes,omitempty"`
		TotalSelectedTests     int      `json:"total_selected_tests,omitempty"`
		TotalSelectedTestClass int      `json:"total_selected_test_classes,omitempty"`
		CPUTimeSaved           int64    `json:"cpu_time_saved,omitempty"`
		IsRunTestV2            bool     `json:"is_run_test_v2,omitempty"`
		Errors                 []string `json:"errors,omitempty"`
	}

	BuildInfo struct {
		HarnessLang      string `json:"harness_lang,omitempty"`
		HarnessBuildTool string `json:"harness_build_tool,omitempty"`
	}

	CacheIntelligenceMetaData struct {
		CacheSize        uint64   `json:"cache_size,omitempty"`
		IsNonDefaultPath bool     `json:"is_non_default_path,omitempty"`
		IsCustomKeys     bool     `json:"is_custom_keys,omitempty"`
		Errors           []string `json:"errors,omitempty"`
	}

	CacheMetadata struct {
		CacheSizeBytes uint64 `json:"cache_size_bytes,omitempty"`
	}
)
