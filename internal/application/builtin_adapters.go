package application

// Keep the full source baseline, but only register the two adapters needed by
// this personal-use distribution. Other platforms must not initialize sessions
// or credential discovery when starting a Channels-only local service.
import (
	_ "wx_channel/internal/adapter/singlefile"
	_ "wx_channel/internal/adapter/wxchannels"
)
