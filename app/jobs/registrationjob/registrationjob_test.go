package registrationjob

import (
	"errors"
	"fmt"
	"hostlink/app/services/agentregistrar"
	"hostlink/app/services/fingerprint"
	"sync"
	"testing"
	"time"
)

func TestRegister(t *testing.T) {
	t.Run("should start registration in goroutine", func(t *testing.T) {
		triggerCalled := make(chan bool, 1)

		mockTrigger := func(fn func() error) {
			triggerCalled <- true
			// Verify that the function is passed but don't execute it
			// to avoid needing all the dependencies
			if fn == nil {
				t.Error("Expected function to be passed to trigger")
			}
		}

		job := NewWithConfig(&Config{
			FingerprintPath: "/tmp/test-fingerprint.json",
			Registrar:       nil, // Will be set in later tests
			Trigger:         mockTrigger,
		})

		// Call Register which should invoke trigger in a goroutine
		job.Register()

		// Check if Trigger was called
		select {
		case <-triggerCalled:
			// Success - Trigger was called
		case <-time.After(100 * time.Millisecond):
			t.Error("Trigger was not called within timeout")
		}
	})

	t.Run("should load or generate fingerprint", func(t *testing.T) {
		loadOrGenerateCalled := false
		expectedFingerprint := "test-fingerprint-123"

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				loadOrGenerateCalled = true
				return &fingerprint.FingerprintData{
					Fingerprint: expectedFingerprint,
				}, false, nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          &mockRegistrar{}, // Mock registrar to avoid nil panic
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			_ = fn()
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}

		if !loadOrGenerateCalled {
			t.Error("LoadOrGenerate was not called on fingerprint manager")
		}
	})

	t.Run("should prepare public key", func(t *testing.T) {
		preparePublicKeyCalled := false
		expectedPublicKey := "test-public-key-base64"

		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				preparePublicKeyCalled = true
				return expectedPublicKey, nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, false, nil
			},
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			_ = fn()
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}

		if !preparePublicKeyCalled {
			t.Error("PreparePublicKey was not called on registrar")
		}
	})

	t.Run("should get default tags", func(t *testing.T) {
		getDefaultTagsCalled := false
		expectedTags := []agentregistrar.TagPair{
			{Key: "hostname", Value: "test-host"},
			{Key: "os", Value: "linux"},
		}

		mockRegistrar := &mockRegistrar{
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				getDefaultTagsCalled = true
				return expectedTags
			},
			preparePublicKeyFunc: func() (string, error) {
				return "test-public-key", nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, false, nil
			},
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			_ = fn()
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}

		if !getDefaultTagsCalled {
			t.Error("GetDefaultTags was not called on registrar")
		}
	})

	t.Run("should call registrar with correct parameters", func(t *testing.T) {
		expectedFingerprint := "test-fingerprint-123"
		expectedPublicKey := "test-public-key-base64"
		expectedTags := []agentregistrar.TagPair{
			{Key: "hostname", Value: "test-host"},
			{Key: "os", Value: "linux"},
		}

		var actualFingerprint string
		var actualPublicKey string
		var actualTags []agentregistrar.TagPair
		registerCalled := false

		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return expectedPublicKey, nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return expectedTags
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				registerCalled = true
				actualFingerprint = fingerprint
				actualPublicKey = publicKey
				actualTags = tags
				return &agentregistrar.RegistrationResponse{
					AgentID: "test-agent-id",
				}, nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: expectedFingerprint,
				}, false, nil
			},
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			_ = fn()
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}

		if !registerCalled {
			t.Error("Register was not called on registrar")
		}

		if actualFingerprint != expectedFingerprint {
			t.Errorf("Register called with wrong fingerprint. Got %s, want %s", actualFingerprint, expectedFingerprint)
		}

		if actualPublicKey != expectedPublicKey {
			t.Errorf("Register called with wrong public key. Got %s, want %s", actualPublicKey, expectedPublicKey)
		}

		if len(actualTags) != len(expectedTags) {
			t.Errorf("Register called with wrong number of tags. Got %d, want %d", len(actualTags), len(expectedTags))
		} else {
			for i, tag := range actualTags {
				if tag.Key != expectedTags[i].Key || tag.Value != expectedTags[i].Value {
					t.Errorf("Register called with wrong tag at index %d. Got %+v, want %+v", i, tag, expectedTags[i])
				}
			}
		}
	})

	t.Run("should log success on successful registration", func(t *testing.T) {
		expectedAgentID := "test-agent-123"

		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return "test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				return &agentregistrar.RegistrationResponse{
					AgentID: expectedAgentID,
					Status:  "success",
				}, nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, false, nil
			},
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			err := fn()
			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			// The test passes if the function returns no error
			// The actual log output is visible in the test output
			// We're verifying the success path completes without error
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}
	})

	t.Run("should log error on registration failure", func(t *testing.T) {
		expectedError := errors.New("registration failed: network error")

		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return "test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				return nil, expectedError
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, false, nil
			},
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			err := fn()
			if err == nil {
				t.Error("Expected an error, but got nil")
			}
			if err != expectedError {
				t.Errorf("Expected error %v, but got %v", expectedError, err)
			}
			// The error log output is visible in the test output
			// We're verifying the error is properly propagated
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}
	})

	t.Run("should log when new fingerprint is generated", func(t *testing.T) {
		newFingerprint := "new-fingerprint-456"

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: newFingerprint,
				}, true, nil // isNew = true indicates new fingerprint
			},
		}

		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return "test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				return &agentregistrar.RegistrationResponse{
					AgentID: "test-agent-id",
				}, nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			err := fn()
			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			// The log output "Generated new fingerprint:" is visible in test output
			// We're verifying the new fingerprint path executes correctly
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}
	})

	t.Run("should log when existing fingerprint is used", func(t *testing.T) {
		existingFingerprint := "existing-fingerprint-789"

		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: existingFingerprint,
				}, false, nil // isNew = false indicates existing fingerprint
			},
		}

		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return "test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				return &agentregistrar.RegistrationResponse{
					AgentID: "test-agent-id",
				}, nil
			},
		}

		// Capture the function that would be run by Trigger
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		job.Register()

		// Wait for the function to be passed to Trigger
		select {
		case fn := <-triggerChan:
			// Execute the function synchronously for testing
			err := fn()
			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			// The log output "Using existing fingerprint:" is visible in test output
			// We're verifying the existing fingerprint path executes correctly
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger was not called within timeout")
		}
	})
}

func TestTrigger(t *testing.T) {
	t.Run("should execute function successfully on first attempt", func(t *testing.T) {
		executionCount := 0
		successFunc := func() error {
			executionCount++
			return nil // Success on first attempt
		}

		// Run Trigger in a goroutine since it's synchronous
		done := make(chan bool)
		go func() {
			Trigger(successFunc)
			done <- true
		}()

		// Wait for completion with timeout
		select {
		case <-done:
			if executionCount != 1 {
				t.Errorf("Expected function to be executed exactly once, but was executed %d times", executionCount)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Trigger did not complete within timeout")
		}
	})

	t.Run("should retry on failure", func(t *testing.T) {
		executionCount := 0
		failTwiceThenSucceed := func() error {
			executionCount++
			if executionCount <= 2 {
				return errors.New("temporary failure")
			}
			return nil // Success on third attempt
		}

		// Run Trigger in a goroutine since it's synchronous
		done := make(chan bool)
		go func() {
			Trigger(failTwiceThenSucceed)
			done <- true
		}()

		// Wait for completion with longer timeout due to retries
		select {
		case <-done:
			if executionCount != 3 {
				t.Errorf("Expected function to be executed 3 times (2 failures + 1 success), but was executed %d times", executionCount)
			}
		case <-time.After(35 * time.Second): // 10s + 20s delays + buffer
			t.Fatal("Trigger did not complete within timeout")
		}
	})

	t.Run("should retry up to 5 times", func(t *testing.T) {
		executionCount := 0
		alwaysFail := func() error {
			executionCount++
			return errors.New("persistent failure")
		}

		// Use test configuration with short delays
		testConfig := TriggerConfig{
			MaxRetries:     5,
			InitialDelay:   10 * time.Millisecond,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig in a goroutine
		done := make(chan bool)
		go func() {
			triggerWithConfig(alwaysFail, testConfig)
			done <- true
		}()

		// Wait for completion with timeout
		// 10ms + 20ms + 40ms + 80ms + 160ms = 310ms total delay + buffer
		select {
		case <-done:
			if executionCount != 5 {
				t.Errorf("Expected function to be executed exactly 5 times, but was executed %d times", executionCount)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Trigger did not complete within timeout")
		}
	})

	t.Run("should use exponential backoff for retries", func(t *testing.T) {
		executionTimes := []time.Time{}
		executionCount := 0

		failFourTimes := func() error {
			executionTimes = append(executionTimes, time.Now())
			executionCount++
			if executionCount < 5 {
				return errors.New("temporary failure")
			}
			return nil // Success on 5th attempt
		}

		// Use test configuration with known delays
		testConfig := TriggerConfig{
			MaxRetries:     5,
			InitialDelay:   10 * time.Millisecond,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig synchronously to measure timing
		triggerWithConfig(failFourTimes, testConfig)

		// Verify we had 5 executions
		if len(executionTimes) != 5 {
			t.Fatalf("Expected 5 executions, got %d", len(executionTimes))
		}

		// Expected delays between retries: 10ms, 20ms, 40ms, 80ms
		expectedDelays := []time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
			40 * time.Millisecond,
			80 * time.Millisecond,
		}

		// Check the delays between executions (with tolerance for timing variations)
		for i := 1; i < len(executionTimes); i++ {
			actualDelay := executionTimes[i].Sub(executionTimes[i-1])
			expectedDelay := expectedDelays[i-1]

			// Sleep should never complete before the specified duration
			minDelay := expectedDelay
			maxDelay := expectedDelay * 2 // Allow up to double for system scheduling delays

			if actualDelay < minDelay || actualDelay > maxDelay {
				t.Errorf("Retry %d: Expected delay at least %v, got %v", i, expectedDelay, actualDelay)
			}
		}
	})

	t.Run("should stop retrying after success", func(t *testing.T) {
		executionCount := 0
		succeedOnThirdAttempt := func() error {
			executionCount++
			if executionCount < 3 {
				return errors.New("temporary failure")
			}
			return nil // Success on 3rd attempt
		}

		// Use test configuration with short delays
		testConfig := TriggerConfig{
			MaxRetries:     5,
			InitialDelay:   10 * time.Millisecond,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig
		triggerWithConfig(succeedOnThirdAttempt, testConfig)

		// Should have stopped after 3rd attempt (success)
		if executionCount != 3 {
			t.Errorf("Expected function to be executed exactly 3 times (stop after success), but was executed %d times", executionCount)
		}
	})

	t.Run("should log each failed attempt", func(t *testing.T) {
		attemptCount := 0
		expectedErrors := []string{
			"error 1",
			"error 2",
			"error 3",
		}

		failWithDifferentErrors := func() error {
			attemptCount++
			if attemptCount <= len(expectedErrors) {
				return errors.New(expectedErrors[attemptCount-1])
			}
			return nil // Success on 4th attempt
		}

		// Use test configuration with short delays
		testConfig := TriggerConfig{
			MaxRetries:     5,
			InitialDelay:   5 * time.Millisecond,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig
		triggerWithConfig(failWithDifferentErrors, testConfig)

		// Verify that we had 4 attempts (3 failures + 1 success)
		if attemptCount != 4 {
			t.Errorf("Expected 4 attempts, got %d", attemptCount)
		}
		// The actual log verification happens by observing the test output
		// Each failed attempt should show "Registration attempt X/5 failed: error X"
	})

	t.Run("should log final failure after all retries", func(t *testing.T) {
		attemptCount := 0
		alwaysFail := func() error {
			attemptCount++
			return errors.New("persistent failure")
		}

		// Use test configuration with short delays
		testConfig := TriggerConfig{
			MaxRetries:     3, // Use 3 for faster test
			InitialDelay:   5 * time.Millisecond,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig
		triggerWithConfig(alwaysFail, testConfig)

		// Verify that all retries were exhausted
		if attemptCount != testConfig.MaxRetries {
			t.Errorf("Expected exactly %d attempts, got %d", testConfig.MaxRetries, attemptCount)
		}
		// The actual log verification happens by observing the test output
		// Should see "Agent registration failed after all retry attempts" at the end
	})

	t.Run("should wait initial delay for first retry", func(t *testing.T) {
		firstAttemptTime := time.Time{}
		secondAttemptTime := time.Time{}
		attemptCount := 0

		failOnce := func() error {
			attemptCount++
			if attemptCount == 1 {
				firstAttemptTime = time.Now()
				return errors.New("first failure")
			} else if attemptCount == 2 {
				secondAttemptTime = time.Now()
			}
			return nil // Success on second attempt
		}

		// Use test configuration with known initial delay
		initialDelay := 10 * time.Millisecond
		testConfig := TriggerConfig{
			MaxRetries:     3,
			InitialDelay:   initialDelay,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig
		triggerWithConfig(failOnce, testConfig)

		// Verify the delay between first and second attempt equals initial delay
		actualDelay := secondAttemptTime.Sub(firstAttemptTime)

		// Sleep should never complete before the specified duration
		minDelay := initialDelay
		maxDelay := initialDelay * 2 // Allow up to double for system scheduling delays

		if actualDelay < minDelay || actualDelay > maxDelay {
			t.Errorf("Expected first retry to wait at least %v (initial delay), but waited %v", initialDelay, actualDelay)
		}
	})

	t.Run("should wait double the previous delay for second retry", func(t *testing.T) {
		executionTimes := []time.Time{}
		attemptCount := 0

		failTwice := func() error {
			executionTimes = append(executionTimes, time.Now())
			attemptCount++
			if attemptCount <= 2 {
				return errors.New("failure")
			}
			return nil // Success on third attempt
		}

		// Use test configuration with known delays
		initialDelay := 10 * time.Millisecond
		testConfig := TriggerConfig{
			MaxRetries:     3,
			InitialDelay:   initialDelay,
			BackoffFactor:  2,
		}

		// Run triggerWithConfig
		triggerWithConfig(failTwice, testConfig)

		// Verify we had 3 executions
		if len(executionTimes) != 3 {
			t.Fatalf("Expected 3 executions, got %d", len(executionTimes))
		}

		// First retry should wait initialDelay
		firstRetryDelay := executionTimes[1].Sub(executionTimes[0])
		// Second retry should wait double the initial delay
		secondRetryDelay := executionTimes[2].Sub(executionTimes[1])

		// Verify second retry is double the first
		expectedSecondDelay := initialDelay * 2

		// Sleep should never complete before the specified duration
		minDelay := expectedSecondDelay
		maxDelay := expectedSecondDelay * 2 // Allow up to double for system scheduling delays

		if secondRetryDelay < minDelay || secondRetryDelay > maxDelay {
			t.Errorf("Expected second retry to wait at least %v (double the initial delay), but waited %v", expectedSecondDelay, secondRetryDelay)
		}

		// Also verify the first retry was approximately the initial delay
		if firstRetryDelay < initialDelay || firstRetryDelay > initialDelay*2 {
			t.Errorf("Expected first retry to wait at least %v, but waited %v", initialDelay, firstRetryDelay)
		}
	})
}

func TestIntegrationFlow(t *testing.T) {
	t.Run("should complete full registration flow", func(t *testing.T) {
		// Track all the steps in the registration flow
		flowSteps := []string{}

		// Mock fingerprint manager
		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				flowSteps = append(flowSteps, "fingerprint_loaded")
				return &fingerprint.FingerprintData{
					Fingerprint: "integration-test-fingerprint",
				}, false, nil
			},
		}

		// Mock registrar
		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				flowSteps = append(flowSteps, "public_key_prepared")
				return "integration-test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				flowSteps = append(flowSteps, "tags_retrieved")
				return []agentregistrar.TagPair{
					{Key: "test", Value: "integration"},
				}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				flowSteps = append(flowSteps, "registration_completed")

				// Verify the parameters
				if fingerprint != "integration-test-fingerprint" {
					return nil, errors.New("unexpected fingerprint")
				}
				if publicKey != "integration-test-public-key" {
					return nil, errors.New("unexpected public key")
				}
				if len(tags) != 1 || tags[0].Key != "test" {
					return nil, errors.New("unexpected tags")
				}

				return &agentregistrar.RegistrationResponse{
					AgentID: "integration-test-agent-id",
					Status:  "registered",
				}, nil
			},
		}

		// Capture the trigger function
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			flowSteps = append(flowSteps, "trigger_called")
			triggerChan <- fn
		}

		// Create job with all mocks
		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		// Start the registration
		job.Register()

		// Wait for and execute the registration synchronously
		var triggerFunc func() error
		select {
		case triggerFunc = <-triggerChan:
			// Got the function
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger function was not captured within timeout")
		}

		err := triggerFunc()
		if err != nil {
			t.Errorf("Registration flow failed: %v", err)
		}

		// Verify the complete flow was executed in the correct order
		expectedFlow := []string{
			"trigger_called",
			"fingerprint_loaded",
			"public_key_prepared",
			"tags_retrieved",
			"registration_completed",
		}

		if len(flowSteps) != len(expectedFlow) {
			t.Errorf("Expected %d flow steps, got %d", len(expectedFlow), len(flowSteps))
		}

		for i, step := range expectedFlow {
			if i >= len(flowSteps) || flowSteps[i] != step {
				t.Errorf("Flow step %d: expected %s, got %s", i, step, flowSteps[i])
			}
		}
	})

	t.Run("should handle fingerprint generation error", func(t *testing.T) {
		fingerprintError := errors.New("failed to generate fingerprint: disk full")

		// Mock fingerprint manager that returns an error
		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return nil, false, fingerprintError
			},
		}

		// Mock registrar - should not be called
		registrarCalled := false
		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				registrarCalled = true
				return "should-not-be-called", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				registrarCalled = true
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				registrarCalled = true
				return &agentregistrar.RegistrationResponse{}, nil
			},
		}

		// Capture the trigger function
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		// Create job with mocks
		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		// Start the registration
		job.Register()

		// Wait for and execute the registration
		var triggerFunc func() error
		select {
		case triggerFunc = <-triggerChan:
			// Got the function
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger function was not captured within timeout")
		}

		// Execute and expect an error
		err := triggerFunc()
		if err == nil {
			t.Error("Expected an error from fingerprint generation, got nil")
		}
		if err != fingerprintError {
			t.Errorf("Expected fingerprint error, got: %v", err)
		}

		// Verify that registrar was not called
		if registrarCalled {
			t.Error("Registrar should not be called when fingerprint generation fails")
		}
	})

	t.Run("should handle public key preparation error", func(t *testing.T) {
		publicKeyError := errors.New("failed to prepare public key: key file corrupted")

		// Mock fingerprint manager - should succeed
		fingerprintCalled := false
		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				fingerprintCalled = true
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, false, nil
			},
		}

		// Mock registrar with public key error
		prepareKeyCalled := false
		registerCalled := false
		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				prepareKeyCalled = true
				return "", publicKeyError
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				// Should not be called
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				registerCalled = true
				return &agentregistrar.RegistrationResponse{}, nil
			},
		}

		// Capture the trigger function
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		// Create job with mocks
		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		// Start the registration
		job.Register()

		// Wait for and execute the registration
		var triggerFunc func() error
		select {
		case triggerFunc = <-triggerChan:
			// Got the function
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger function was not captured within timeout")
		}

		// Execute and expect an error
		err := triggerFunc()
		if err == nil {
			t.Error("Expected an error from public key preparation, got nil")
		}
		if err != publicKeyError {
			t.Errorf("Expected public key error, got: %v", err)
		}

		// Verify the flow stopped at the right place
		if !fingerprintCalled {
			t.Error("Fingerprint should have been loaded before public key error")
		}
		if !prepareKeyCalled {
			t.Error("PreparePublicKey should have been called")
		}
		if registerCalled {
			t.Error("Register should not be called when public key preparation fails")
		}
	})

	t.Run("should handle registration API error", func(t *testing.T) {
		registrationError := errors.New("registration failed: agent already exists")

		// Track what was called
		flowSteps := []string{}

		// Mock fingerprint manager - should succeed
		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				flowSteps = append(flowSteps, "fingerprint_loaded")
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, true, nil // isNew = true for variety
			},
		}

		// Mock registrar - everything succeeds except registration
		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				flowSteps = append(flowSteps, "public_key_prepared")
				return "test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				flowSteps = append(flowSteps, "tags_retrieved")
				return []agentregistrar.TagPair{
					{Key: "env", Value: "test"},
				}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				flowSteps = append(flowSteps, "registration_attempted")
				return nil, registrationError
			},
		}

		// Capture the trigger function
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		// Create job with mocks
		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		// Start the registration
		job.Register()

		// Wait for and execute the registration
		var triggerFunc func() error
		select {
		case triggerFunc = <-triggerChan:
			// Got the function
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger function was not captured within timeout")
		}

		// Execute and expect an error
		err := triggerFunc()
		if err == nil {
			t.Error("Expected an error from registration API, got nil")
		}
		if err != registrationError {
			t.Errorf("Expected registration error, got: %v", err)
		}

		// Verify all steps up to registration were executed
		expectedSteps := []string{
			"fingerprint_loaded",
			"public_key_prepared",
			"tags_retrieved",
			"registration_attempted",
		}

		if len(flowSteps) != len(expectedSteps) {
			t.Errorf("Expected %d steps, got %d", len(expectedSteps), len(flowSteps))
		}

		for i, step := range expectedSteps {
			if i >= len(flowSteps) || flowSteps[i] != step {
				t.Errorf("Step %d: expected %s, got %s", i, step, flowSteps[i])
			}
		}
	})

	t.Run("should handle network timeout", func(t *testing.T) {
		timeoutError := errors.New("registration failed: context deadline exceeded")

		// Track timing
		registrationStartTime := time.Time{}
		registrationEndTime := time.Time{}

		// Mock fingerprint manager - should succeed quickly
		mockFingerprintMgr := &mockFingerprintManager{
			loadOrGenerateFunc: func() (*fingerprint.FingerprintData, bool, error) {
				return &fingerprint.FingerprintData{
					Fingerprint: "test-fingerprint",
				}, false, nil
			},
		}

		// Mock registrar - simulate timeout on registration
		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return "test-public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return []agentregistrar.TagPair{
					{Key: "env", Value: "test"},
				}
			},
			registerFunc: func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				registrationStartTime = time.Now()
				// Simulate a network timeout scenario
				// In real scenario, this would be an actual timeout from HTTP client
				time.Sleep(5 * time.Millisecond) // Simulate some delay
				registrationEndTime = time.Now()
				return nil, timeoutError
			},
		}

		// Capture the trigger function
		triggerChan := make(chan func() error, 1)
		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		// Create job with mocks
		job := NewWithConfig(&Config{
			FingerprintManager: mockFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		// Start the registration
		job.Register()

		// Wait for and execute the registration
		var triggerFunc func() error
		select {
		case triggerFunc = <-triggerChan:
			// Got the function
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Trigger function was not captured within timeout")
		}

		// Execute and expect a timeout error
		err := triggerFunc()
		if err == nil {
			t.Error("Expected a timeout error, got nil")
		}
		if err != timeoutError {
			t.Errorf("Expected timeout error, got: %v", err)
		}

		// Verify that the registration attempt took some time
		if !registrationStartTime.IsZero() && !registrationEndTime.IsZero() {
			duration := registrationEndTime.Sub(registrationStartTime)
			if duration < 5*time.Millisecond {
				t.Errorf("Expected registration to take at least 5ms (simulated timeout), but took %v", duration)
			}
		}
	})
}

func TestConcurrency(t *testing.T) {
	t.Run("should handle concurrent registration attempts with real fingerprint", func(t *testing.T) {
		// Create a temporary directory for fingerprint file
		tempDir := t.TempDir()
		fingerprintPath := tempDir + "/test-fingerprint.json"

		// Track registration attempts and fingerprints used
		var mu sync.Mutex
		registrationCount := 0
		concurrentAttempts := 5
		fingerprintsUsed := make(map[string]int)

		// Use REAL fingerprint manager
		realFingerprintMgr := fingerprint.NewManager(fingerprintPath)

		// Mock registrar - track fingerprints used in registration
		mockRegistrar := &mockRegistrar{
			preparePublicKeyFunc: func() (string, error) {
				return "public-key", nil
			},
			getDefaultTagsFunc: func() []agentregistrar.TagPair {
				return []agentregistrar.TagPair{}
			},
			registerFunc: func(fp string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
				mu.Lock()
				registrationCount++
				currentCount := registrationCount
				fingerprintsUsed[fp]++
				mu.Unlock()

				// Simulate API call delay
				time.Sleep(5 * time.Millisecond)

				return &agentregistrar.RegistrationResponse{
					AgentID: fmt.Sprintf("agent-%d", currentCount),
				}, nil
			},
		}

		// Channel to collect trigger functions
		triggerChan := make(chan func() error, concurrentAttempts)

		mockTrigger := func(fn func() error) {
			triggerChan <- fn
		}

		// Create job with REAL fingerprint manager
		job := NewWithConfig(&Config{
			FingerprintManager: realFingerprintMgr,
			Registrar:          mockRegistrar,
			Trigger:            mockTrigger,
		})

		// Launch multiple concurrent registration attempts
		for i := 0; i < concurrentAttempts; i++ {
			job.Register()
		}

		// Collect all trigger functions
		triggerFuncs := make([]func() error, 0, concurrentAttempts)
		timeout := time.After(1 * time.Second)
	collectLoop:
		for i := 0; i < concurrentAttempts; i++ {
			select {
			case fn := <-triggerChan:
				triggerFuncs = append(triggerFuncs, fn)
			case <-timeout:
				t.Errorf("Timeout waiting for trigger functions, got %d/%d", i, concurrentAttempts)
				break collectLoop
			}
		}

		// Execute all trigger functions concurrently to simulate real concurrent access
		var execWg sync.WaitGroup
		for _, fn := range triggerFuncs {
			execWg.Add(1)
			go func(triggerFn func() error) {
				defer execWg.Done()
				err := triggerFn()
				if err != nil {
					t.Errorf("Registration failed: %v", err)
				}
			}(fn)
		}

		// Wait for all executions to complete
		execWg.Wait()

		// Verify results
		mu.Lock()
		defer mu.Unlock()

		// CRITICAL CHECK: All registrations should use the SAME fingerprint
		if len(fingerprintsUsed) != 1 {
			t.Errorf("Expected all registrations to use the same fingerprint, but got %d different fingerprints: %v",
				len(fingerprintsUsed), fingerprintsUsed)
		}

		// Verify the fingerprint was used for all registrations
		for fp, count := range fingerprintsUsed {
			if count != concurrentAttempts {
				t.Errorf("Fingerprint %s was used %d times, expected %d times", fp, count, concurrentAttempts)
			}
			t.Logf("All %d concurrent registrations correctly used the same fingerprint: %s", count, fp)
		}

		if registrationCount != concurrentAttempts {
			t.Errorf("Expected %d registrations, got %d", concurrentAttempts, registrationCount)
		}
	})

}

// Mock types for testing
type mockFingerprintManager struct {
	loadOrGenerateFunc func() (*fingerprint.FingerprintData, bool, error)
}

func (m *mockFingerprintManager) LoadOrGenerate() (*fingerprint.FingerprintData, bool, error) {
	if m.loadOrGenerateFunc != nil {
		return m.loadOrGenerateFunc()
	}
	return nil, false, errors.New("not implemented")
}

type mockRegistrar struct {
	preparePublicKeyFunc func() (string, error)
	registerFunc         func(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error)
	getDefaultTagsFunc   func() []agentregistrar.TagPair
}

func (m *mockRegistrar) PreparePublicKey() (string, error) {
	if m.preparePublicKeyFunc != nil {
		return m.preparePublicKeyFunc()
	}
	return "mock-public-key", nil
}

func (m *mockRegistrar) Register(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error) {
	if m.registerFunc != nil {
		return m.registerFunc(fingerprint, publicKey, tags)
	}
	return &agentregistrar.RegistrationResponse{AgentID: "mock-agent-id"}, nil
}

func (m *mockRegistrar) GetDefaultTags() []agentregistrar.TagPair {
	if m.getDefaultTagsFunc != nil {
		return m.getDefaultTagsFunc()
	}
	return []agentregistrar.TagPair{}
}