package serial

import "testing"

func TestDefaultConfigIsValid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate(): %v", err)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	base := DefaultConfig()

	cases := []func(*Config){
		func(c *Config) { c.Baud = 0 },
		func(c *Config) { c.Baud = -9600 },
		func(c *Config) { c.DataBits = 4 },
		func(c *Config) { c.DataBits = 9 },
		func(c *Config) { c.Parity = "weird" },
		func(c *Config) { c.StopBits = "3" },
		func(c *Config) { c.FlowControl = "carrier-pigeon" },
	}
	for i, mutate := range cases {
		c := base
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: Validate() = nil, want error for %+v", i, c)
		}
	}
}

func TestFrameString(t *testing.T) {
	cases := []struct {
		cfg  Config
		want string
	}{
		{DefaultConfig(), "8N1"},
		{Config{DataBits: 7, Parity: ParityEven, StopBits: StopBits2}, "7E2"},
		{Config{DataBits: 8, Parity: ParityOdd, StopBits: StopBits1_5}, "8O1.5"},
		{Config{DataBits: 8, Parity: ParityMark, StopBits: StopBits1}, "8M1"},
		{Config{DataBits: 8, Parity: ParitySpace, StopBits: StopBits1}, "8S1"},
	}
	for _, c := range cases {
		if got := c.cfg.FrameString(); got != c.want {
			t.Errorf("FrameString(%+v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}

func TestBaudPresetsAreAllValid(t *testing.T) {
	for _, baud := range BaudPresets {
		c := DefaultConfig()
		c.Baud = baud
		if err := c.Validate(); err != nil {
			t.Errorf("preset baud %d: Validate(): %v", baud, err)
		}
	}
}
