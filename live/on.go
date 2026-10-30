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

// Value marks an element as a listener of a value topic: value frames for key
// set its data-value attribute, which CSS styles. current is the value at
// render. Any number of elements may listen to the same key:
//
//	<span class="data-[value=idle]:bg-amber-400" { live.Value("presence", p.ID, p.Presence)... }></span>
func Value(topic, key, current string) gsx.Attrs {
	return gsx.Attrs{
		{Key: "data-live-value", Value: topic},
		{Key: "data-live-value-key", Value: key},
		{Key: "data-value", Value: current},
	}
}

// ValueText marks an element whose text is a value topic's value for key:
// value frames replace its text content.
//
//	<span { live.ValueText("presence", p.ID)... }>{ p.Presence }</span>
func ValueText(topic, key string) gsx.Attrs {
	return gsx.Attrs{
		{Key: "data-live-value", Value: topic},
		{Key: "data-live-value-key", Value: key},
		{Key: "data-live-value-into", Value: "text"},
	}
}
