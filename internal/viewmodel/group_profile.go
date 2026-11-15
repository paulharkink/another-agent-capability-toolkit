package viewmodel

import (
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
)

type GroupProfileSnapshot struct {
	PackID     string
	Profiles   []GroupProfile
	Candidates []GroupProfileMember
	Errors     []string
	ObservedAt time.Time
}

type GroupProfile struct {
	PackID, Name, Description, Origin, Error string
	Editable                                 bool
	Members                                  []GroupProfileMember
}

type GroupProfileMember struct {
	Ref                               config.InstanceRef
	Name, ConfigStatus, RuntimeStatus string
	Status, URL, Error                string
	MCPs                              []RuntimeStatus
}

type GroupProfileUpdate struct {
	PackID  string
	Name    string
	Members []config.InstanceRef
}

type GroupProfileLaunchResult struct {
	Name, Status, Step, Error string
	MCPs                      []GroupProfileRuntimeResult
}

type GroupProfileRuntimeResult struct {
	CapabilityID, InstanceName, MCPName string
	Status, URL, Error                  string
}
