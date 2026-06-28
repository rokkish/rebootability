package injector

import (
	"context"
	"time"
)

// Event は1回の注入で発生した最大2つのタイムスタンプを返す。
//
//   - InjectAt  : 注入の起点 (recovery time の基準)。停止操作が始まった時刻。
//   - StartAt   : 外部から start を発行した時刻。kill-only / restart モードでは zero。
//   - Mode      : このイベントを生み出した injector の識別子。trial に保存し、
//     後段の分析で「何を測ったか」を取り違えないようにする。
type Event struct {
	InjectAt time.Time
	StartAt  time.Time
	Mode     string
}

// Injector は SUT に停止操作を加える外部観察者。回復行為は原則として
// 実装の責務ではない。kill-start のように「停止 + 外部主導の再起動」を
// 一連の注入として測りたい場合だけ StartAt を埋める。
type Injector interface {
	Inject(ctx context.Context) (Event, error)
	Mode() string
}
