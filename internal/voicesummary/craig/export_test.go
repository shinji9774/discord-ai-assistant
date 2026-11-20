package craig

import "time"

// ErrJobBusy はテストから errJobBusy を判定するために公開する。
var ErrJobBusy = errJobBusy

// SetPollInterval はテストでジョブの確認間隔を縮める。
func SetPollInterval(c *Client, d time.Duration) {
	c.pollInterval = d
}
