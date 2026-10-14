package app

import "context"

func (s *Service) PackSettings(ctx context.Context) (map[string]string, error) {
	settings, err := s.UISettings(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"capability-pack": s.Source.ID, "pack-directory": s.Source.Root, "catalog-file": settings["catalog-file"], "instance-directory": s.Source.InstanceRoot, "profile-directory": s.Source.InstanceRoot, "state-directory": s.Store.Root(), "default-agents": settings["default_agents"]}
	return out, nil
}
