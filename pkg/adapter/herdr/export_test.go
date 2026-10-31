package herdr

import "time"

// SetTimeout shortens the request timeout in tests.
func SetTimeout(c *Client, d time.Duration) { c.timeout = d }
