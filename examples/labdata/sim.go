package labdata

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnknownAction is returned by Simulate for an action it does not know.
var ErrUnknownAction = errors.New("unknown action")

// Publish announces that topic changed for ids (none: the whole topic).
type Publish func(topic string, ids ...string)

// Simulate applies one "other user" action from the simulate panel to s and
// announces it with publish, the same way in every stage. The burst runs in
// the background: fifty rows, one every 2 ms.
func Simulate(s *Store, action, id string, publish Publish) error {
	switch action {
	case "reset":
		s.Reset()
		publish("resync")
	case "series-touch":
		ids := strings.Fields(id)
		for _, id := range ids {
			if err := s.TouchSeries(id); err != nil {
				return err
			}
		}
		publish("lab:series", ids...)
	case "presence":
		if err := s.CyclePresence(id); err != nil {
			return err
		}
		publish("lab:presence", id)
	case "draft":
		d, err := s.Draft(id)
		if err != nil {
			return err
		}
		if err := s.SaveDraft(id, fmt.Sprint(len(d.Value)*1000+7), "Grace"); err != nil {
			return err
		}
		publish("lab:draft", id)
	case "file-add":
		f := s.AddFile(fmt.Sprintf("upload-%d.pdf", time.Now().UnixMilli()%100000))
		publish("lab:files", f.ID)
	case "file-delete":
		if err := s.DeleteFile(id); err != nil {
			return err
		}
		publish("lab:files", id)
	case "burst":
		go func() {
			for _, row := range s.Burst()[:50] {
				if s.BumpBurst(row.ID) == nil {
					publish("lab:burst", row.ID)
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
	case "job-advance":
		if err := s.AdvanceJob(id); err != nil {
			return err
		}
		publish("lab:job-progress", id)
	case "job-step":
		if err := s.StepJob(id); err != nil {
			return err
		}
		publish("lab:job", id)
	default:
		return fmt.Errorf("%w %q", ErrUnknownAction, action)
	}
	return nil
}
