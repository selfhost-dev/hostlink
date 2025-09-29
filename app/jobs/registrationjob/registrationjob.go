package registrationjob

import (
	"hostlink/app/services/agentregistrar"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/fingerprint"
	"hostlink/config/appconf"

	"github.com/labstack/gommon/log"
)

type TriggerFunc func(func() error)

type FingerprintManager interface {
	LoadOrGenerate() (*fingerprint.FingerprintData, bool, error)
}

type Registrar interface {
	PreparePublicKey() (string, error)
	Register(fingerprint string, publicKey string, tags []agentregistrar.TagPair) (*agentregistrar.RegistrationResponse, error)
	GetDefaultTags() []agentregistrar.TagPair
}

type Job struct {
	trigger        TriggerFunc
	fingerprintMgr FingerprintManager
	registrar      Registrar
	agentState     *agentstate.AgentState
}

type Config struct {
	FingerprintPath    string
	FingerprintManager FingerprintManager
	Registrar          Registrar
	AgentState         *agentstate.AgentState
	Trigger            TriggerFunc
}

func New() *Job {
	return NewWithConfig(&Config{
		FingerprintPath: appconf.AgentFingerprintPath(),
		Registrar:       agentregistrar.New(),
		Trigger:         Trigger,
	})
}

func NewWithConfig(cfg *Config) *Job {
	if cfg.Trigger == nil {
		cfg.Trigger = Trigger
	}

	fingerprintMgr := cfg.FingerprintManager
	if fingerprintMgr == nil {
		fingerprintMgr = fingerprint.NewManager(cfg.FingerprintPath)
	}

	registrar := cfg.Registrar
	if registrar == nil {
		registrar = agentregistrar.New()
	}

	return &Job{
		trigger:        cfg.Trigger,
		fingerprintMgr: fingerprintMgr,
		registrar:      registrar,
		agentState:     cfg.AgentState,
	}
}

func (j *Job) Register() {
	go j.trigger(func() error {
		fingerprintData, isNew, err := j.fingerprintMgr.LoadOrGenerate()
		if err != nil {
			log.Errorf("Failed to load/generate fingerprint: %v", err)
			return err
		}

		if isNew {
			log.Info("Generated new fingerprint:", fingerprintData.Fingerprint)
		} else {
			log.Info("Using existing fingerprint:", fingerprintData.Fingerprint)
		}

		// Check if agent is already registered (after fingerprint is loaded)
		if j.agentState != nil && !isNew {
			// Only check for existing registration if fingerprint is not new
			if err := j.agentState.Load(); err == nil && j.agentState.GetAgentID() != "" {
				log.Infof("Agent already registered with ID: %s", j.agentState.GetAgentID())
				return nil
			}
			// If load fails or no agent ID exists, proceed with registration
		}

		publicKey, err := j.registrar.PreparePublicKey()
		if err != nil {
			log.Errorf("Failed to prepare public key: %v", err)
			return err
		}

		tags := j.registrar.GetDefaultTags()

		response, err := j.registrar.Register(fingerprintData.Fingerprint, publicKey, tags)
		if err != nil {
			log.Errorf("Registration failed: %v", err)
			return err
		}

		log.Infof("Agent registered successfully: %s", response.AgentID)

		// Save agent ID to state if state manager is configured
		if j.agentState != nil {
			if err := j.agentState.SetAgentID(response.AgentID); err != nil {
				log.Errorf("Failed to save agent ID to state: %v", err)
				// Don't fail the registration if state save fails
			}
		}

		return nil
	})
}

