package storage

import (
	"context"
	"time"
)

// panicStorage 实现 Storage 接口，所有方法 panic。
// 嵌入到 mock struct 中，只需覆盖测试关注的方法。
type panicStorage struct{}

func (panicStorage) Init() error                         { panic("not implemented") }
func (panicStorage) Close() error                        { panic("not implemented") }
func (panicStorage) Ping() error                         { panic("not implemented") }
func (panicStorage) WithContext(context.Context) Storage { panic("not implemented") }
func (panicStorage) SaveRecord(*ProbeRecord) error       { panic("not implemented") }
func (panicStorage) GetLatest(string, string, string, string) (*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetHistory(string, string, string, string, time.Time) ([]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetHistoryWithLimit(string, string, string, string, time.Time, int) ([]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetLatestByModelID(string) (*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetHistoryByModelID(string, time.Time) ([]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetHistoryWithLimitByModelID(string, time.Time, int) ([]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetLatestBatch([]MonitorKey) (map[MonitorKey]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetHistoryBatch([]MonitorKey, time.Time) (map[MonitorKey][]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetLatestBatchByModelID([]ProbeHistoryKey) (map[ProbeHistoryKey]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) GetHistoryBatchByModelID([]ProbeHistoryKey, time.Time) (map[ProbeHistoryKey][]*ProbeRecord, error) {
	panic("not implemented")
}
func (panicStorage) MigrateChannelData([]ChannelMigrationMapping) error { panic("not implemented") }
func (panicStorage) BackfillProbeHistoryModelIDs([]ModelIDMigrationMapping) error {
	panic("not implemented")
}
func (panicStorage) GetServiceState(string, string, string, string) (*ServiceState, error) {
	panic("not implemented")
}
func (panicStorage) UpsertServiceState(*ServiceState) error { panic("not implemented") }
func (panicStorage) GetChannelState(string, string, string) (*ChannelState, error) {
	panic("not implemented")
}
func (panicStorage) UpsertChannelState(*ChannelState) error { panic("not implemented") }
func (panicStorage) GetModelStatesForChannel(string, string, string) ([]*ServiceState, error) {
	panic("not implemented")
}
func (panicStorage) SaveStatusEvent(*StatusEvent) error { panic("not implemented") }
func (panicStorage) GetStatusEvents(int64, int, *EventFilters) ([]*StatusEvent, error) {
	panic("not implemented")
}
func (panicStorage) GetLatestEventID() (int64, error) { panic("not implemented") }
func (panicStorage) PurgeOldRecords(context.Context, time.Time, int) (int64, error) {
	panic("not implemented")
}
