package main

import (
	"database/sql"
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

func errToMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (t *TrackerFil) Close() error {
	return t.db.Close()
}
