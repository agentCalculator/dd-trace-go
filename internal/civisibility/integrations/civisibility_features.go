// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/impactedtests"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	DefaultFlakyRetryCount      = 5
	DefaultFlakyTotalRetryCount = 1_000
)

type (
	// FlakyRetriesSetting struct to hold all the settings related to flaky tests retries
	FlakyRetriesSetting struct {
		RetryCount               int64
		TotalRetryCount          int64
		RemainingTotalRetryCount int64
	}

	searchCommitsResponse struct {
		LocalCommits  []string
		RemoteCommits []string
		IsOk          bool
	}
)

var (
	// settingsInitializationOnce ensures we do the settings initialization just once
	settingsInitializationOnce sync.Once

	// additionalFeaturesInitializationOnce ensures we do the additional features initialization just once
	additionalFeaturesInitializationOnce sync.Once

	// ciVisibilityRapidClient contains the http rapid client to do CI Visibility queries and upload to the rapid backend
	ciVisibilityClient net.Client

	// ciVisibilitySettings contains the CI Visibility settings for this session
	ciVisibilitySettings net.SettingsResponseData

	// ciVisibilityKnownTests contains the CI Visibility Known Tests data for this session
	ciVisibilityKnownTests net.KnownTestsResponseData

	// ciVisibilityFlakyRetriesSettings contains the CI Visibility Flaky Retries settings for this session
	ciVisibilityFlakyRetriesSettings FlakyRetriesSetting

	// ciVisibilitySkippables contains the CI Visibility skippable tests for this session
	ciVisibilitySkippables map[string]map[string][]net.SkippableResponseDataAttributes

	// ciVisibilityTestManagementTests contains the CI Visibility test management tests for this session
	ciVisibilityTestManagementTests net.TestManagementTestsResponseDataModules

	// ciVisibilityImpactedTestsAnalyzer contains the CI Visibility impacted tests analyzer
	ciVisibilityImpactedTestsAnalyzer *impactedtests.ImpactedTestAnalyzer
)

func ensureSettingsInitialization(serviceName string) {
	settingsInitializationOnce.Do(func() {
		log.Debug("civisibility: initializing settings")

		// Create the CI Visibility client
		ciVisibilityClient = net.NewClientWithServiceName(serviceName)
		if ciVisibilityClient == nil {
			log.Error("civisibility: error getting the ci visibility http client")
			return
		}

		// upload the repository changes
		var uploadChannel = make(chan struct{})
		go func() {
			bytes, err := uploadRepositoryChanges()
			if err != nil {
				log.Error("civisibility: error uploading repository changes: %v", err)
			} else {
				log.Debug("civisibility: uploaded %v bytes in pack files", bytes)
			}
			uploadChannel <- struct{}{}
		}()

		// Get the CI Visibility settings payload for this test session
		ciSettings, err := ciVisibilityClient.GetSettings()
		if err != nil {
			log.Error("civisibility: error getting CI visibility settings: %v", err)
		} else if ciSettings != nil {
			ciVisibilitySettings = *ciSettings
		}

		// check if we need to wait for the upload to finish and repeat the settings request or we can just continue
		if ciVisibilitySettings.RequireGit {
			log.Debug("civisibility: waiting for the git upload to finish and repeating the settings request")
			<-uploadChannel
			ciSettings, err = ciVisibilityClient.GetSettings()
			if err != nil {
				log.Error("civisibility: error getting CI visibility settings: %v", err)
			} else if ciSettings != nil {
				ciVisibilitySettings = *ciSettings
			}
		} else if ciVisibilitySettings.ImpactedTestsEnabled {
			log.Debug("civisibility: impacted tests is enabled we need to wait for the upload to finish (for the unshallow process)")
			<-uploadChannel
		} else {
			log.Debug("civisibility: no need to wait for the git upload to finish")
			// Enqueue a close action to wait for the upload to finish before finishing the process
			PushCiVisibilityCloseAction(func() {
				<-uploadChannel
			})
		}
	})
}

// ensureAdditionalFeaturesInitialization initialize all the additional features
func ensureAdditionalFeaturesInitialization(serviceName string) {
	additionalFeaturesInitializationOnce.Do(func() {
		log.Debug("civisibility: initializing additional features")
		ensureSettingsInitialization(serviceName)
		if ciVisibilityClient == nil {
			return
		}

		// map to store the additional tags we want to add (Capabilities and CorrelationId)
		additionalTags := make(map[string]string)
		defer func() {
			if len(additionalTags) > 0 {
				log.Debug("civisibility: adding additional tags: %v", additionalTags)
				utils.AddCITagsMap(additionalTags)
			}
		}()

		// set the default values for the additional tags
		additionalTags[constants.LibraryCapabilitiesEarlyFlakeDetection] = "1"
		additionalTags[constants.LibraryCapabilitiesAutoTestRetries] = "1"
		additionalTags[constants.LibraryCapabilitiesTestImpactAnalysis] = "1"
		additionalTags[constants.LibraryCapabilitiesTestManagementQuarantine] = "1"
		additionalTags[constants.LibraryCapabilitiesTestManagementDisable] = "1"
		additionalTags[constants.LibraryCapabilitiesTestManagementAttemptToFix] = "2"

		// mutex to protect the additional tags map
		var aTagsMutex sync.Mutex
		// function to set additional tags locking with the mutex
		setAdditionalTags := func(key string, value string) {
			aTagsMutex.Lock()
			defer aTagsMutex.Unlock()
			additionalTags[key] = value
		}

		// wait group to wait for all the additional features to be loaded
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			// if early flake detection is enabled then we run the known tests request
			if ciVisibilitySettings.KnownTestsEnabled {
				ciEfdData, err := ciVisibilityClient.GetKnownTests()
				if err != nil {
					log.Error("civisibility: error getting CI visibility known tests data: %v", err)
				} else if ciEfdData != nil {
					ciVisibilityKnownTests = *ciEfdData
					log.Debug("civisibility: known tests data loaded.")
				}
			} else {
				// "known_tests_enabled" parameter works as a kill-switch for EFD, so if “known_tests_enabled” is false it
				// will disable EFD even if “early_flake_detection.enabled” is set to true (which should not happen normally,
				// the backend should disable both of them in that case)
				ciVisibilitySettings.EarlyFlakeDetection.Enabled = false
			}
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			// if flaky test retries is enabled then let's load the flaky retries settings
			if ciVisibilitySettings.FlakyTestRetriesEnabled {
				flakyRetryEnabledByEnv := internal.BoolEnv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, true)
				if flakyRetryEnabledByEnv {
					totalRetriesCount := (int64)(internal.IntEnv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, DefaultFlakyTotalRetryCount))
					retryCount := (int64)(internal.IntEnv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, DefaultFlakyRetryCount))
					ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{
						RetryCount:               retryCount,
						TotalRetryCount:          totalRetriesCount,
						RemainingTotalRetryCount: totalRetriesCount,
					}
					log.Debug("civisibility: automatic test retries enabled [retryCount: %v, totalRetryCount: %v]", retryCount, totalRetriesCount)
				} else {
					log.Warn("civisibility: flaky test retries was disabled by the environment variable")
					ciVisibilitySettings.FlakyTestRetriesEnabled = false
				}
			}
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			// if ITR is enabled then we do the skippable tests request
			if ciVisibilitySettings.TestsSkipping {
				// get the skippable tests
				correlationID, skippableTests, err := ciVisibilityClient.GetSkippableTests()
				if err != nil {
					log.Error("civisibility: error getting CI visibility skippable tests: %v", err)
				} else if skippableTests != nil {
					log.Debug("civisibility: skippable tests loaded: %d suites", len(skippableTests))
					setAdditionalTags(constants.ItrCorrelationIDTag, correlationID)
					ciVisibilitySkippables = skippableTests
				}
			}
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			// if test management is enabled then we check if it was disabled by the environment variable
			if ciVisibilitySettings.TestManagement.Enabled {
				testManagementEnabledByEnv := internal.BoolEnv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, true)
				testManagementAttemptToFixRetriesEnv := internal.IntEnv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, -1)
				if testManagementEnabledByEnv {
					if testManagementAttemptToFixRetriesEnv != -1 {
						ciVisibilitySettings.TestManagement.AttemptToFixRetries = testManagementAttemptToFixRetriesEnv
					}

					testManagementTests, err := ciVisibilityClient.GetTestManagementTests()
					if err != nil {
						log.Error("civisibility: error getting CI visibility test management tests: %v", err)
					} else if testManagementTests != nil {
						ciVisibilityTestManagementTests = *testManagementTests
						log.Debug("civisibility: test management loaded [attemptToFixRetries: %v]", ciVisibilitySettings.TestManagement.AttemptToFixRetries)
					}
				} else {
					ciVisibilitySettings.TestManagement.Enabled = false
					log.Warn("civisibility: test management was disabled by the environment variable")
				}
			}
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			// if wheter the settings response or the env var is true we load the impacted tests analyzer
			if ciVisibilitySettings.ImpactedTestsEnabled ||
				internal.BoolEnv(constants.CIVisibilityImpactedTestsDetectionEnabled, false) {
				var iTests *impactedtests.ImpactedTestAnalyzer
				var err error
				if ciVisibilitySettings.ImpactedTestsEnabled {
					// backend returned enabled = true, we pass the client to the analyzer for backend requests
					iTests, err = impactedtests.NewImpactedTestAnalyzer(ciVisibilityClient)
				} else {
					// only local diff (not using the backend response)
					iTests, err = impactedtests.NewImpactedTestAnalyzer(nil)
				}
				if err != nil {
					log.Error("civisibility: error getting CI visibility impacted tests analyzer: %v", err)
				} else {
					ciVisibilityImpactedTestsAnalyzer = iTests
					log.Debug("civisibility: impacted tests analyzer loaded")
				}
			}
			wg.Done()
		}()

		// wait for all the additional features to be loaded
		wg.Wait()
	})
}

// GetSettings gets the settings from the backend settings endpoint
func GetSettings() *net.SettingsResponseData {
	// call to ensure the settings features initialization is completed (service name can be null here)
	ensureSettingsInitialization("")
	return &ciVisibilitySettings
}

// GetKnownTests gets the known tests data
func GetKnownTests() *net.KnownTestsResponseData {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityKnownTests
}

// GetTestManagementTestsData gets the test management tests data
func GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityTestManagementTests
}

// GetFlakyRetriesSettings gets the flaky retries settings
func GetFlakyRetriesSettings() *FlakyRetriesSetting {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityFlakyRetriesSettings
}

// GetSkippableTests gets the skippable tests from the backend
func GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return ciVisibilitySkippables
}

// GetImpactedTestsAnalyzer gets the impacted tests analyzer
func GetImpactedTestsAnalyzer() *impactedtests.ImpactedTestAnalyzer {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return ciVisibilityImpactedTestsAnalyzer
}

func uploadRepositoryChanges() (bytes int64, err error) {
	// get the search commits response
	initialCommitData, err := getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err.Error())
	}

	// let's check if we could retrieve commit data
	if !initialCommitData.IsOk {
		return 0, nil
	}

	// if there are no commits then we don't need to do anything
	if !initialCommitData.hasCommits() {
		log.Debug("civisibility: no commits found")
		return 0, nil
	}

	// If:
	//   - we have local commits
	//   - there are not missing commits (backend has the total number of local commits already)
	// then we are good to go with it, we don't need to check if we need to unshallow or anything and just go with that.
	if initialCommitData.hasCommits() && len(initialCommitData.missingCommits()) == 0 {
		log.Debug("civisibility: initial commit data has everything already, we don't need to upload anything")
		return 0, nil
	}

	// there's some missing commits on the backend, first we need to check if we need to unshallow before sending anything...
	hasBeenUnshallowed, err := utils.UnshallowGitRepository()
	if err != nil || !hasBeenUnshallowed {
		if err != nil {
			log.Warn("%v", err)
		}
		// if unshallowing the repository failed or if there's nothing to unshallow then we try to upload the packfiles from
		// the initial commit data

		// send the pack file with the missing commits
		return sendObjectsPackFile(initialCommitData.LocalCommits[0], initialCommitData.missingCommits(), initialCommitData.RemoteCommits)
	}

	// after unshallowing the repository we need to get the search commits to calculate the missing commits again
	commitsData, err := getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err.Error())
	}

	// let's check if we could retrieve commit data
	if !initialCommitData.IsOk {
		return 0, nil
	}

	// send the pack file with the missing commits
	return sendObjectsPackFile(commitsData.LocalCommits[0], commitsData.missingCommits(), commitsData.RemoteCommits)
}

// getSearchCommits gets the search commits response with the local and remote commits
func getSearchCommits() (*searchCommitsResponse, error) {
	localCommits := utils.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		log.Debug("civisibility: no local commits found")
		return newSearchCommitsResponse(nil, nil, false), nil
	}

	log.Debug("civisibility: local commits found: %d", len(localCommits))
	remoteCommits, err := ciVisibilityClient.GetCommits(localCommits)
	return newSearchCommitsResponse(localCommits, remoteCommits, true), err
}

// newSearchCommitsResponse creates a new search commits response
func newSearchCommitsResponse(localCommits []string, remoteCommits []string, isOk bool) *searchCommitsResponse {
	return &searchCommitsResponse{
		LocalCommits:  localCommits,
		RemoteCommits: remoteCommits,
		IsOk:          isOk,
	}
}

// hasCommits returns true if the search commits response has commits
func (r *searchCommitsResponse) hasCommits() bool {
	return len(r.LocalCommits) > 0
}

// missingCommits returns the missing commits between the local and remote commits
func (r *searchCommitsResponse) missingCommits() []string {
	var missingCommits []string
	for _, localCommit := range r.LocalCommits {
		if !slices.Contains(r.RemoteCommits, localCommit) {
			missingCommits = append(missingCommits, localCommit)
		}
	}

	return missingCommits
}

func sendObjectsPackFile(commitSha string, commitsToInclude []string, commitsToExclude []string) (bytes int64, err error) {
	// get the pack files to send
	packFiles := utils.CreatePackFiles(commitsToInclude, commitsToExclude)
	if len(packFiles) == 0 {
		log.Debug("civisibility: no pack files to send")
		return 0, nil
	}

	// send the pack files
	log.Debug("civisibility: sending pack file with missing commits. files: %v", packFiles)

	// try to remove the pack files after sending them
	defer func(files []string) {
		// best effort to remove the pack files after sending
		for _, file := range files {
			_ = os.Remove(file)
		}
	}(packFiles)

	// send the pack files
	return ciVisibilityClient.SendPackFiles(commitSha, packFiles)
}
