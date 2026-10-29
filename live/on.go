package live

import "github.com/gsxhq/gsx"

// On marks an element live: the dispatcher refetches it when a frame of
// topic arrives. With a key, only frames naming that key (or naming nothing)
// match. Spread it on the element:
//
//	<tr id={ Page.Row |> key(row.ID) } { live.On("orbit:data", row.ID)... }>
func On(topic string, key ...string) gsx.Attrs {
	a := gsx.Attrs{{Key: "data-live", Value: topic}}
	if len(key) > 0 {
		a = append(a, gsx.Attr{Key: "data-live-key", Value: key[0]})
	}
	return a
}
