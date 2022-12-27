package main

import (
	"database/sql"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
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

create index if not exists provider_pings_provider_index
    on provider_pings (provider);

create index if not exists provider_pings_ts_index
    on provider_pings (ts desc);

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

	dealid integer
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
	_, err := t.db.Exec("insert into providers (address) values (?) on conflict do nothing", a.String())
	return err
}

func (t *TrackerFil) RecordPing(p address.Address, took time.Duration, err error) {
	_, qerr := t.db.Exec("insert into provider_pings (provider, ts, success, took_us, msg) values (?, ?, ?, ?, ?)",
		p.String(), time.Now().Unix(), err == nil, took.Microseconds(), errToMsg(err))
	if qerr != nil {
		log.Warnf("failed to record ping: %s", qerr)
	}
}

func (t *TrackerFil) RecordRetrieval(p address.Address, err error, blocks, bytes int64, dealid abi.DealID) error {
	_, qerr := t.db.Exec("insert into retrieval_stats (provider, success, msg, blocks, bytes, dealid) values (?, ?, ?, ?, ?, ?)",
		p.String(), err == nil, errToMsg(err), blocks, bytes, dealid)
	return qerr
}

const slowPingThreshold = 2 * time.Second

// gets stats for the last 24 hours
func (t *TrackerFil) ProviderPingStats(p address.Address) (success, fail, slow int64, err error) {
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

func (t *TrackerFil) AllProviderPingStats() (map[address.Address]*ProviderPingStats, error) {
	rows, err := t.db.Query("select provider, success, fail, slow from (select provider, count(*) as success from provider_pings where success = 1 group by provider) left join (select provider, count(*) as fail from provider_pings where success = 0 group by provider) using (provider) left join (select provider, count(*) as slow from provider_pings where success = 1 and took_us > ? group by provider) using (provider)", slowPingThreshold.Microseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[address.Address]*ProviderPingStats)
	for rows.Next() {
		var p string
		var s, f, sl *int64
		err := rows.Scan(&p, &s, &f, &sl)
		if err != nil {
			return nil, err
		}

		if s == nil {
			s = new(int64)
		}
		if f == nil {
			f = new(int64)
		}
		if sl == nil {
			sl = new(int64)
		}

		addr, err := address.NewFromString(p)
		if err != nil {
			return nil, err
		}

		out[addr] = &ProviderPingStats{
			Success: *s,
			Fail:    *f,
			Slow:    *sl,

			IsHealthy: *s > *f,
			IsSlow:    *sl > *s/3,

			SuccessPct: fmt.Sprintf("%.2f%%", float64(*s)/float64(*s+*f)*100),
			SlowPct:    fmt.Sprintf("%.2f%%", float64(*sl)/float64(*s)*100),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
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
