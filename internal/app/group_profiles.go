package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// UIGroupProfileSnapshot joins pack declarations and local membership edits to
// the current Capability Instance and runtime observations.
func (s *Service) UIGroupProfileSnapshot(ctx context.Context) (viewmodel.GroupProfileSnapshot, error) {
	out := viewmodel.GroupProfileSnapshot{PackID: s.Source.ID, Profiles: []viewmodel.GroupProfile{}, Candidates: []viewmodel.GroupProfileMember{}, Errors: []string{}, ObservedAt: time.Now()}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	local, err := s.Store.GroupProfiles(s.Source.ID)
	if err != nil {
		return out, err
	}
	instances, err := s.UICapabilityInstances(ctx)
	if err != nil {
		return out, err
	}
	byRef := make(map[config.InstanceRef]viewmodel.CapabilityInstance)
	for capabilityID, snapshot := range instances {
		for _, instance := range snapshot.Instances {
			byRef[instance.Ref] = instance
			member := memberFromInstance(instance)
			out.Candidates = append(out.Candidates, member)
		}
		for _, message := range snapshot.Errors {
			out.Errors = append(out.Errors, capabilityID+": "+message)
		}
	}
	sort.Slice(out.Candidates, func(i, j int) bool {
		a, b := out.Candidates[i].Ref, out.Candidates[j].Ref
		if a.CapabilityID != b.CapabilityID {
			return a.CapabilityID < b.CapabilityID
		}
		return a.Name < b.Name
	})
	localByName := make(map[string]state.GroupProfile, len(local))
	for _, profile := range local {
		localByName[strings.ToLower(profile.Name)] = profile
	}
	usedLocal := map[string]bool{}
	for _, packProfile := range s.Source.GroupProfiles {
		profile := viewmodel.GroupProfile{PackID: s.Source.ID, Name: packProfile.Name, Description: packProfile.Description, Origin: "pack", Error: packProfile.Error, Editable: true}
		members := packProfile.Members
		if edit, ok := localByName[strings.ToLower(packProfile.Name)]; ok {
			members = groupMemberRefs(s.Source.ID, edit.Members)
			usedLocal[strings.ToLower(edit.Name)] = true
		}
		profile.Members = groupProfileMembers(members, byRef)
		out.Profiles = append(out.Profiles, profile)
	}
	for _, saved := range local {
		key := strings.ToLower(saved.Name)
		if usedLocal[key] {
			continue
		}
		profile := viewmodel.GroupProfile{PackID: s.Source.ID, Name: saved.Name, Origin: "local", Editable: true, Members: groupProfileMembers(groupMemberRefs(s.Source.ID, saved.Members), byRef)}
		out.Profiles = append(out.Profiles, profile)
	}
	sort.Slice(out.Profiles, func(i, j int) bool {
		return strings.ToLower(out.Profiles[i].Name) < strings.ToLower(out.Profiles[j].Name)
	})
	return out, nil
}

// UISetGroupProfile saves only local profile membership. Pack declarations and
// Capability Instance settings remain read-only and in their existing stores.
func (s *Service) UISetGroupProfile(ctx context.Context, update viewmodel.GroupProfileUpdate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if update.PackID != s.Source.ID {
		return fmt.Errorf("Capability Pack %q is not selected", update.PackID)
	}
	snapshot, err := s.UIGroupProfileSnapshot(ctx)
	if err != nil {
		return err
	}
	knownProfile := false
	for _, profile := range snapshot.Profiles {
		if strings.EqualFold(profile.Name, update.Name) {
			knownProfile = true
			if profile.Name != update.Name {
				return fmt.Errorf("Profile name %q conflicts with existing Profile %q", update.Name, profile.Name)
			}
			break
		}
	}
	if !knownProfile {
		for _, profile := range snapshot.Profiles {
			if strings.EqualFold(profile.Name, update.Name) {
				return fmt.Errorf("Profile %q already exists", update.Name)
			}
		}
	}
	candidates := make(map[config.InstanceRef]bool, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		candidates[candidate.Ref] = true
	}
	members := make([]state.GroupProfileMember, 0, len(update.Members))
	seen := make(map[config.InstanceRef]bool)
	for _, member := range update.Members {
		if member.PackID == "" {
			member.PackID = s.Source.ID
		}
		if member.PackID != s.Source.ID || !candidates[member] {
			return fmt.Errorf("Capability Instance %s/%s is not in the active Pack", member.CapabilityID, member.Name)
		}
		if seen[member] {
			return fmt.Errorf("duplicate Capability Instance %s/%s", member.CapabilityID, member.Name)
		}
		seen[member] = true
		members = append(members, state.GroupProfileMember{CapabilityID: member.CapabilityID, InstanceName: member.Name})
	}
	return s.Store.SaveGroupProfile(state.GroupProfile{PackID: s.Source.ID, Name: update.Name, Members: members})
}

// UIDeleteGroupProfile deletes a user Profile or restores a pack Profile's
// default membership by removing its local override.
func (s *Service) UIDeleteGroupProfile(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !groupProfileNameExists(s.Source.GroupProfiles, name) {
		local, err := s.Store.GroupProfiles(s.Source.ID)
		if err != nil {
			return err
		}
		found := false
		for _, profile := range local {
			if profile.Name == name {
				found = true
				break
			}
		}
		if !found {
			return errors.New("Profile no longer exists")
		}
	}
	return s.Store.RemoveGroupProfile(s.Source.ID, name)
}

// UIGroupProfileLaunch starts each MCP in one Profile in membership order.
// A member failure is recorded and does not prevent later members from running.
func (s *Service) UIGroupProfileLaunch(ctx context.Context, name string) ([]viewmodel.GroupProfileLaunchResult, error) {
	return s.launchGroupProfile(ctx, name, nil)
}

// UIGroupProfileRetry repeats the launch for one member while retaining the
// same continuation and readiness rules as Launch all.
func (s *Service) UIGroupProfileRetry(ctx context.Context, name string, ref config.InstanceRef) ([]viewmodel.GroupProfileLaunchResult, error) {
	return s.launchGroupProfile(ctx, name, &ref)
}

func (s *Service) launchGroupProfile(ctx context.Context, name string, only *config.InstanceRef) ([]viewmodel.GroupProfileLaunchResult, error) {
	snapshot, err := s.UIGroupProfileSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	var selected *viewmodel.GroupProfile
	for i := range snapshot.Profiles {
		if strings.EqualFold(snapshot.Profiles[i].Name, name) {
			selected = &snapshot.Profiles[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("Profile %q no longer exists", name)
	}
	if selected.Error != "" {
		return nil, fmt.Errorf("Profile %q is invalid: %s", name, selected.Error)
	}
	if only != nil {
		found := false
		for _, member := range selected.Members {
			if member.Ref == *only {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("Capability Instance %s/%s is not a member of Profile %q", only.CapabilityID, only.Name, name)
		}
	}
	observer := viewmodel.OperationProgressObserver(ctx)
	var scoped *Service
	progressCtx := ctx
	if observer != nil {
		scoped, progressCtx = s.withOperationProgress(ctx, observer)
	} else {
		scoped = s
	}
	results := make([]viewmodel.GroupProfileLaunchResult, 0, len(selected.Members))
	appendResult := func(member viewmodel.GroupProfileMember, result viewmodel.GroupProfileLaunchResult) {
		results = append(results, result)
		if observer != nil {
			observer(viewmodel.OperationProgress{ProfileName: selected.Name, MemberName: member.Name, MemberStatus: result.Status, MemberNeedsInput: result.Status == "needs-input" || groupProfileNeedsInput(result.MCPs), InstanceRef: member.Ref, Step: member.Name + " · " + result.Status})
		}
	}
	for _, member := range selected.Members {
		if only != nil && member.Ref != *only {
			continue
		}
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := viewmodel.GroupProfileLaunchResult{Name: member.Name, Status: "failed", MCPs: []viewmodel.GroupProfileRuntimeResult{}}
		switch member.Status {
		case "missing":
			result.Status, result.Error = "skipped", member.Error
			appendResult(member, result)
			continue
		case "invalid":
			result.Status, result.Error = "failed", member.Error
			appendResult(member, result)
			continue
		case "needs input":
			result.Status, result.Error = "needs-input", member.Error
			appendResult(member, result)
			continue
		}
		pkg, instance, parent, loadErr := scoped.loadInstance(member.Ref)
		if loadErr != nil {
			result.Status = groupProfileRunFailureStatus(loadErr)
			result.Error = loadErr.Error()
			appendResult(member, result)
			continue
		}
		pkg, instance, values, _, loadErr := scoped.instanceValuesForPreview(pkg, instance, parent, InstanceRequest{Ref: member.Ref})
		if loadErr != nil {
			result.Status, result.Error = groupProfileRunFailureStatus(loadErr), loadErr.Error()
			appendResult(member, result)
			continue
		}
		definitions := packageMCPProfiles(pkg, values)
		if len(definitions) == 0 {
			result.Status = "skipped"
			result.Error = "Capability Instance has no MCP servers to launch"
			appendResult(member, result)
			continue
		}
		for _, definition := range definitions {
			if err := ctx.Err(); err != nil {
				return results, err
			}
			step := fmt.Sprintf("Starting %s · %s", member.Name, definition.Name)
			if observer != nil {
				observer(viewmodel.OperationProgress{Step: step})
			}
			run, runErr := scoped.RunInstanceMCP(progressCtx, "start", InstanceRequest{Ref: member.Ref}, definition.Name)
			childKey := mcpDefinitionKey(parent, pkg, definition)
			runtimeStatus, runtimeURL, observeErr := observedRuntimeStatus(ctx, scoped.Options.Runtime, childKey)
			mcpResult := viewmodel.GroupProfileRuntimeResult{CapabilityID: member.Ref.CapabilityID, InstanceName: member.Ref.Name, MCPName: definition.Name}
			switch {
			case runErr != nil:
				mcpResult.Status, mcpResult.Error = groupProfileRunFailureStatus(runErr), runErr.Error()
			case observeErr != nil:
				mcpResult.Status, mcpResult.Error = "unknown", "Could not verify readiness: "+observeErr.Error()
			case runtimeStatus != "running":
				mcpResult.Status = "not-ready"
				mcpResult.Error = fmt.Sprintf("runtime observation is %s", runtimeStatus)
			case definition.HealthPath != "":
				headerValues, headerErr := profileRegistrationHeaders(definition, values)
				if headerErr != nil {
					mcpResult.Status, mcpResult.Error = "needs-input", "Authentication inputs are required: "+headerErr.Error()
					result.MCPs = append(result.MCPs, mcpResult)
					continue
				}
				headers := make(http.Header, len(headerValues))
				for name, value := range headerValues {
					headers.Set(name, value)
				}
				ready, healthErr := mcp.WaitForAuthentication(ctx, runtimeURL, definition.HealthPath, headers)
				if healthErr != nil || !ready {
					mcpResult.Status = "needs-input"
					if healthErr != nil {
						mcpResult.Error = "Authentication readiness check failed: " + healthErr.Error()
					} else {
						mcpResult.Error = "Authentication is required"
					}
				} else {
					mcpResult.Status, mcpResult.URL = "ready", runtimeURL
				}
			default:
				mcpResult.Status, mcpResult.URL = "ready", runtimeURL
			}
			if runErr != nil && run.Step != "" {
				mcpResult.Error = run.Step + ": " + mcpResult.Error
			}
			result.MCPs = append(result.MCPs, mcpResult)
		}
		result.Status, result.Error = aggregateGroupLaunchResults(result.MCPs)
		appendResult(member, result)
	}
	return results, nil
}

func groupProfileRunFailureStatus(err error) string {
	var invalidInput *InvalidInput
	var authRequired authenticationRequiredFailure
	if errors.As(err, &invalidInput) || errors.As(err, &authRequired) || strings.Contains(strings.ToLower(err.Error()), "authentication required") {
		return "needs-input"
	}
	return "failed"
}

func observedRuntimeStatus(ctx context.Context, runtime MCPRuntime, key state.Key) (string, string, error) {
	instances, err := runtime.List(ctx)
	if err != nil {
		return "unknown", "", err
	}
	status := "missing"
	url := ""
	for _, instance := range instances {
		if instance.Key != key {
			continue
		}
		if instance.Status == "running" {
			status, url = instance.Status, instance.URL
			if instance.Ownership != "local" {
				status = "unowned"
			}
			return status, url, nil
		}
		status = instance.Status
	}
	return status, url, nil
}

func aggregateGroupLaunchResults(results []viewmodel.GroupProfileRuntimeResult) (string, string) {
	ready, needsInput, failed := 0, 0, 0
	errors := []string{}
	for _, result := range results {
		switch result.Status {
		case "ready":
			ready++
		case "needs-input":
			needsInput++
		case "failed", "unknown", "not-ready":
			failed++
		}
		if result.Error != "" {
			errors = append(errors, result.MCPName+": "+result.Error)
		}
	}
	switch {
	case ready == len(results):
		return "ready", ""
	case ready > 0:
		return "partial", strings.Join(errors, "\n")
	case needsInput > 0 && failed == 0:
		return "needs-input", strings.Join(errors, "\n")
	case failed > 0:
		return "failed", strings.Join(errors, "\n")
	default:
		return "skipped", "no MCP servers to launch"
	}
}

func groupProfileNeedsInput(results []viewmodel.GroupProfileRuntimeResult) bool {
	for _, result := range results {
		if result.Status == "needs-input" {
			return true
		}
	}
	return false
}

func groupProfileNameExists(profiles []config.GroupProfile, name string) bool {
	for _, profile := range profiles {
		if strings.EqualFold(profile.Name, name) {
			return true
		}
	}
	return false
}

func groupMemberRefs(packID string, members []state.GroupProfileMember) []config.InstanceRef {
	refs := make([]config.InstanceRef, 0, len(members))
	for _, member := range members {
		refs = append(refs, config.InstanceRef{PackID: packID, CapabilityID: member.CapabilityID, Name: member.InstanceName})
	}
	return refs
}

func groupProfileMembers(refs []config.InstanceRef, byRef map[config.InstanceRef]viewmodel.CapabilityInstance) []viewmodel.GroupProfileMember {
	members := make([]viewmodel.GroupProfileMember, 0, len(refs))
	for _, ref := range refs {
		if ref.PackID == "" {
			continue
		}
		instance, ok := byRef[ref]
		if !ok {
			members = append(members, viewmodel.GroupProfileMember{Ref: ref, Name: ref.CapabilityID + "/" + ref.Name, ConfigStatus: "missing", Status: "missing", Error: "Capability Instance was not found in the active Pack"})
			continue
		}
		members = append(members, memberFromInstance(instance))
	}
	return members
}

func memberFromInstance(instance viewmodel.CapabilityInstance) viewmodel.GroupProfileMember {
	member := viewmodel.GroupProfileMember{Ref: instance.Ref, Name: instance.Ref.CapabilityID + "/" + instance.Ref.Name, ConfigStatus: instance.ConfigStatus, MCPs: append([]viewmodel.RuntimeStatus{}, instance.MCPs...)}
	switch {
	case instance.ConfigStatus == "needs-input":
		member.Status = "needs input"
		member.Error = instance.ConfigError
	case instance.ConfigStatus == "invalid":
		member.Status = "invalid"
		member.Error = instance.ConfigError
	case len(instance.MCPs) == 0:
		member.Status = "not launched"
	case anyAuthenticationStatus(instance.MCPs, viewmodel.AuthenticationFailed, viewmodel.AuthenticationNotConfirmed):
		member.Status = "needs input"
		member.Error = "MCP authentication has not completed"
	case allRuntimeStatus(instance.MCPs, "running"):
		member.Status = "ready"
	case anyRuntimeStatus(instance.MCPs, "running"):
		member.Status = "partial"
	case anyRuntimeStatus(instance.MCPs, "unavailable", "unknown", "conflict"):
		member.Status = "unknown"
	default:
		member.Status = "stopped"
	}
	for _, runtime := range instance.MCPs {
		if runtime.URL != "" {
			member.URL = runtime.URL
		}
		if runtime.Error != "" {
			member.Error = runtime.Error
		}
		if member.RuntimeStatus == "" || runtime.Status == "running" {
			member.RuntimeStatus = runtime.Status
		}
	}
	return member
}

func anyAuthenticationStatus(runtimes []viewmodel.RuntimeStatus, statuses ...string) bool {
	for _, runtime := range runtimes {
		for _, status := range statuses {
			if runtime.AuthenticationStatus == status {
				return true
			}
		}
	}
	return false
}

func allRuntimeStatus(runtimes []viewmodel.RuntimeStatus, status string) bool {
	if len(runtimes) == 0 {
		return false
	}
	for _, runtime := range runtimes {
		if runtime.Status != status {
			return false
		}
	}
	return true
}

func anyRuntimeStatus(runtimes []viewmodel.RuntimeStatus, statuses ...string) bool {
	for _, runtime := range runtimes {
		for _, status := range statuses {
			if runtime.Status == status {
				return true
			}
		}
	}
	return false
}
