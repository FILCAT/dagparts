package main

import (
	"database/sql"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/chain/types"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
create table if not exists providers
(
    address text
        constraint miners_pk
            primary key
);

create table if not exists provider_pings
(
    provider text    not null
        constraint provider_pings_providers_address_fk
            references providers,
    ts       integer not null,
    success  integer not null,
    took_us  integer not null,
    msg      text,
    constraint provider_pings_pk
        primary key (provider, ts)
);

create unique index if not exists provider_pings_ts_provider_index
    on provider_pings (ts, provider);

create table if not exists retrieval_stats
(
    id       integer not null
        constraint retrieval_stats_pk
            primary key autoincrement,
    provider text    not null
        constraint retrieval_stats_providers_address_fk
            references providers,
    success  integer not null,
    msg      text,

    ts       integer default (unixepoch()) not null,

    blocks   integer,
    bytes    integer,
    
    piece text
);

create unique index if not exists retrieval_stats_id_uindex
    on retrieval_stats (id);

create index if not exists retrieval_stats_provider_index
    on retrieval_stats (provider);

create index if not exists retrieval_stats_ts_index
    on retrieval_stats (ts desc);
`

type TrackerFil struct {
	db *sql.DB

	dblk sync.RWMutex
}

func OpenFilTracker(root string) (*TrackerFil, error) {
	db, err := sql.Open("sqlite3", root)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(schema)
	if err != nil {
		return nil, err
	}

	return &TrackerFil{db: db}, nil
}

func (t *TrackerFil) UpsertProvider(a address.Address) error {
	t.dblk.Lock()
	defer t.dblk.Unlock()

	_, err := t.db.Exec("insert into providers (address) values (?) on conflict do nothing", a.String())
	return err
}

func (t *TrackerFil) RecordPing(p address.Address, took time.Duration, err error) {
	t.dblk.Lock()
	defer t.dblk.Unlock()

	_, qerr := t.db.Exec("insert into provider_pings (provider, ts, success, took_us, msg) values (?, ?, ?, ?, ?)",
		p.String(), time.Now().Unix(), err == nil, took.Microseconds(), errToMsg(err))
	if qerr != nil {
		log.Warnf("failed to record ping: %s", qerr)
	}
}

func (t *TrackerFil) RecordRetrieval(p address.Address, err error, successCode int, bytes int64, piece string) {
	t.dblk.Lock()
	defer t.dblk.Unlock()

	_, qerr := t.db.Exec("insert into retrieval_stats (provider, success, msg, bytes, piece) values (?, ?, ?, ?, ?)",
		p.String(), successCode, errToMsg(err), bytes, piece)
	if qerr != nil {
		log.Warnf("failed to record retrieval: %s", qerr)
	}
}

const slowPingThreshold = 2 * time.Second

// gets stats for the last 24 hours
func (t *TrackerFil) ProviderPingStats(p address.Address) (success, fail, slow int64, err error) {
	t.dblk.RLock()
	defer t.dblk.RUnlock()

	startTime := time.Now().Add(-24 * time.Hour).Unix()
	err = t.db.QueryRow("select count(*) from provider_pings where provider = ? and ts > ? and success = 1", p.String(), startTime).Scan(&success)
	if err != nil {
		return
	}

	err = t.db.QueryRow("select count(*) from provider_pings where provider = ? and ts > ? and success = 0", p.String(), startTime).Scan(&fail)
	if err != nil {
		return
	}

	err = t.db.QueryRow("select count(*) from provider_pings where provider = ? and ts > ? and success = 1 and took_us > ?", p.String(), startTime, slowPingThreshold.Microseconds()).Scan(&slow)
	if err != nil {
		return
	}
	return
}

type ProviderPingStats struct {
	Success int64
	Fail    int64
	Slow    int64

	IsHealthy, IsSlow bool

	SuccessPct, SlowPct string
}

func (t *TrackerFil) AllProviderPingStats(d time.Duration) (map[address.Address]*ProviderPingStats, error) {
	t.dblk.RLock()
	defer t.dblk.RUnlock()

	startTime := time.Now()

	minTs := time.Now().Add(-d).Unix()

	out := make(map[address.Address]*ProviderPingStats)
	// in 3 separate queries for performance reasons

	rows, err := t.db.Query("select provider, count(*) as success from provider_pings where ts > ? and success = 1 group by provider", minTs)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var p string
		var s int64
		err := rows.Scan(&p, &s)
		if err != nil {
			return nil, err
		}

		addr, err := address.NewFromString(p)
		if err != nil {
			return nil, err
		}

		out[addr] = &ProviderPingStats{
			Success: s,
		}
	}

	rows, err = t.db.Query("select provider, count(*) as fail from provider_pings where ts > ? and success = 0 group by provider", minTs)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var p string
		var f int64
		err := rows.Scan(&p, &f)
		if err != nil {
			return nil, err
		}

		addr, err := address.NewFromString(p)
		if err != nil {
			return nil, err
		}

		if out[addr] == nil {
			out[addr] = &ProviderPingStats{}
		}

		out[addr].Fail = f
	}

	rows, err = t.db.Query("select provider, count(*) as slow from provider_pings where ts > ? and success = 1 and took_us > ? group by provider", minTs, slowPingThreshold.Microseconds())
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var p string
		var sl int64
		err := rows.Scan(&p, &sl)
		if err != nil {
			return nil, err
		}

		addr, err := address.NewFromString(p)
		if err != nil {
			return nil, err
		}

		if out[addr] == nil {
			out[addr] = &ProviderPingStats{}
		}

		out[addr].Slow = sl
	}

	for addr, stats := range out {
		stats.IsHealthy = stats.Success > stats.Fail
		stats.IsSlow = stats.Slow > stats.Success/3

		stats.SuccessPct = fmt.Sprintf("%.2f%%", float64(stats.Success)/float64(stats.Success+stats.Fail)*100)
		stats.SlowPct = fmt.Sprintf("%.2f%%", float64(stats.Slow)/float64(stats.Success)*100)

		out[addr] = stats
	}

	log.Infof("pings query took %s", time.Since(startTime))

	return out, nil
}

type RetrievalStats struct {
	Success int64
	Fail    int64

	IsHealthy bool

	SuccessPct string

	Bytes string
}

func (t *TrackerFil) AllProviderRetrievalStats() (map[address.Address]*RetrievalStats, error) {
	t.dblk.RLock()
	defer t.dblk.RUnlock()

	startTime := time.Now()

	rows, err := t.db.Query("select provider, success, fail, bytes from (select provider, count(*) as success, sum(bytes) as bytes from retrieval_stats where success = 0 group by provider) full outer join (select provider, count(*) as fail from retrieval_stats where success > 0 group by provider) using (provider)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[address.Address]*RetrievalStats)
	for rows.Next() {
		var p string
		var s, f *int64
		var b *int64
		err := rows.Scan(&p, &s, &f, &b)
		if err != nil {
			return nil, err
		}

		if s == nil {
			s = new(int64)
		}
		if f == nil {
			f = new(int64)
		}
		if b == nil {
			b = new(int64)
		}

		addr, err := address.NewFromString(p)
		if err != nil {
			return nil, err
		}

		out[addr] = &RetrievalStats{
			Success: *s,
			Fail:    *f,

			IsHealthy: *s > *f,

			SuccessPct: fmt.Sprintf("%.2f%%", float64(*s)/float64(*s+*f)*100),

			Bytes: types.SizeStr(types.NewInt(uint64(*b))),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	log.Infof("retrievals query took %s", time.Since(startTime))

	return out, nil
}

type RetrLog struct {
	At      time.Time
	Success int
	Msg     string
}

type PingLog struct {
	At      time.Time
	Success bool
	Msg     string
}

type ProviderDetails struct {
	RetrSuccess, RetrFail int64
	PingSuccess, PingFail int64

	RecentRetrievals []RetrLog
	RecentPings      []PingLog
}

func (t *TrackerFil) ProviderDetails(p address.Address) (*ProviderDetails, error) {
	t.dblk.RLock()
	defer t.dblk.RUnlock()

	var retrSuccess, retrFail, pingSuccess, pingFail int64
	err := t.db.QueryRow("select count(*) from retrieval_stats where provider = ? and success = 0", p.String()).Scan(&retrSuccess)
	if err != nil {
		return nil, err
	}
	err = t.db.QueryRow("select count(*) from retrieval_stats where provider = ? and success > 0", p.String()).Scan(&retrFail)
	if err != nil {
		return nil, err
	}
	err = t.db.QueryRow("select count(*) from provider_pings where provider = ? and success = 1", p.String()).Scan(&pingSuccess)
	if err != nil {
		return nil, err
	}
	err = t.db.QueryRow("select count(*) from provider_pings where provider = ? and success = 0", p.String()).Scan(&pingFail)
	if err != nil {
		return nil, err
	}

	retrRows, err := t.db.Query("select ts, success, msg from retrieval_stats where provider = ? order by ts desc limit 40", p.String())
	if err != nil {
		return nil, err
	}
	defer retrRows.Close()

	var retrLogs []RetrLog
	for retrRows.Next() {
		var ts int64
		var success int
		var msg string
		err := retrRows.Scan(&ts, &success, &msg)
		if err != nil {
			return nil, err
		}

		retrLogs = append(retrLogs, RetrLog{
			At:      time.Unix(ts, 0),
			Success: success,
			Msg:     msg,
		})
	}
	if err := retrRows.Err(); err != nil {
		return nil, err
	}

	pingRows, err := t.db.Query("select ts, success, msg from provider_pings where provider = ? order by ts desc limit 20", p.String())
	if err != nil {
		return nil, err
	}
	defer pingRows.Close()

	var pingLogs []PingLog
	for pingRows.Next() {
		var ts int64
		var success bool
		var msg string
		err := pingRows.Scan(&ts, &success, &msg)
		if err != nil {
			return nil, err
		}

		pingLogs = append(pingLogs, PingLog{
			At:      time.Unix(ts, 0),
			Success: success,
			Msg:     msg,
		})
	}
	if err := pingRows.Err(); err != nil {
		return nil, err
	}

	return &ProviderDetails{
		RetrSuccess: retrSuccess,
		RetrFail:    retrFail,
		PingSuccess: pingSuccess,
		PingFail:    pingFail,

		RecentRetrievals: retrLogs,
		RecentPings:      pingLogs,
	}, nil
}

func errToMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (t *TrackerFil) Close() error {
	return t.db.Close()
}
