package jobs

// Real-data verification of how the capability scaler reads pool state.
//
// It replays one scale window against Postgres: every capability group of a real pool file
// (prod1's) is scaled by 4 runner pods with 30 concurrent jobs each, as the outbox processor
// does, on a database holding instance rows with the real prod1 mix and real row widths. It
// compares the previous reads (List the pool per group, count setup jobs per slot) with the
// current ones, and checks that both compute the same free and used counts.
//
// Run with:
//
//	REALDATA_PG_DSN='postgres://postgres@127.0.0.1:55432/dlite_test?sslmode=disable' \
//	REALDATA_POOL_FILE=/path/to/pool.yml go test ./app/scheduler/jobs -run TestRealDataCapabilityReads -v

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database"
	dbsql "github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	realPods         = 4  // runner pods in prod2
	realJobsPerPod   = 30 // outbox batch size: concurrent jobs a pod runs
	realScaleWindows = 576
	realBurstRepeats = 5
	// prod1's disabled pools.
	realDisabledPools = "windows-amd64,windows-amd64-fallback," +
		"linux-amd64-fallback,linux-amd64-west4,linux-amd64-east5,linux-amd64-fallback-aws," +
		"linux-arm64-fallback,linux-arm64-asia-southeast1,linux-arm64-europe-west4"
	realSecretBytesCA  = 227 // measured on prod1 dlite_1.instances: avg octet_length of each column
	realSecretBytesCrt = 570
)

// realGroup is a (pool, tenant, variant, image, state) group of instances measured on prod1
// dlite_1.instances (2026-11-25 06:14 UTC, 213 rows, average row 2076 bytes).
type realGroup struct {
	pool, tenant, variant, image string
	state                        types.InstanceState
	n                            int
}

const (
	imgAmd = "cie-hosted-vm-paid-prod/global/images/byoi-harness-cloud-ubuntu22-amd64-v20261026"
	imgArm = "cie-hosted-vm-paid-prod/global/images/byoi-harness-cloud-ubuntu22-arm64-v20261026"
	imgWin = "cie-hosted-vm-paid-prod/global/images/byoi-hosted-vm-windows22-v20261030"
	imgFrA = "cie-hosted-vm-freemium-prod/global/images/byoi-harness-cloud-ubuntu22-amd64-v20261026"
	imgFrW = "cie-hosted-vm-freemium-prod/global/images/byoi-hosted-vm-windows22-v20261030"
)

var realProd1Mix = []realGroup{
	{"linux-amd64", "default", "variant_medium", imgAmd, types.StateInUse, 57},
	{"linux-amd64", "default", "variant_medium", imgAmd, types.StateCreated, 52},
	{"linux-arm64", "default", "variant_xlarge", imgArm, types.StateCreated, 33},
	{"windows-free-amd64", "default", "default", imgFrW, types.StateCreated, 14},
	{"linux-amd64-free", "default", "default", imgFrA, types.StateInUse, 8},
	{"linux-arm64", "default", "variant_xlarge", imgArm, types.StateInUse, 7},
	{"linux-amd64", "default", "variant_xlarge", imgAmd, types.StateCreated, 4},
	{"linux-amd64", "default", "variant_large", imgAmd, types.StateCreated, 4},
	{"linux-amd64", "default", "variant_medium", imgAmd, types.StateTerminating, 4},
	{"linux-arm64", "default", "variant_large", imgArm, types.StateCreated, 3},
	{"linux-amd64-fallback", "default", "variant_large", imgAmd, types.StateCreated, 2},
	{"windows-amd64", "default", "variant_medium", imgWin, types.StateCreated, 2},
	{"linux-amd64-free", "default", "default", imgFrA, types.StateCreated, 2},
	{"linux-amd64", "default", "variant_xxxlarge", imgAmd, types.StateCreated, 2},
	{"windows-free-amd64", "default", "default", imgFrW, types.StateProvisioning, 2},
	{"linux-amd64", "default", "variant_xsmall", imgAmd, types.StateCreated, 2},
	{"linux-arm64", "default", "variant_medium", imgArm, types.StateCreated, 2},
	{"linux-amd64", "default", "variant_xsmall", imgAmd, types.StateInUse, 2},
	{"linux-amd64-west4", "default", "variant_xxxlarge", imgAmd, types.StateInUse, 1},
	{"windows-amd64", "default", "variant_small", imgWin, types.StateCreated, 1},
	{"linux-amd64", "default", "variant_large", imgAmd, types.StateInUse, 1},
	{"linux-amd64", "default", "variant_medium", imgAmd, types.StateProvisioning, 1},
	{"windows-amd64", "default", "variant_large", imgWin, types.StateCreated, 1},
	{"linux-amd64", "default", "variant_xxxlarge", imgAmd, types.StateInUse, 1},
	{"linux-amd64", "default", "variant_xxlarge", imgAmd, types.StateCreated, 1},
	{"linux-amd64", "default", "variant_small", imgAmd, types.StateCreated, 1},
	{"linux-arm64-asia-southeast1", "default", "variant_xlarge", imgArm, types.StateInUse, 1},
	{"linux-amd64", "default", "variant_small_hw_ubuntu24", imgAmd, types.StateCreated, 1},
	{"linux-arm64", "default", "variant_large", imgArm, types.StateTerminating, 1},
}

// countingDialer counts the bytes a connection pool reads from Postgres.
type countingDialer struct{ read *atomic.Int64 }

type countingConn struct {
	net.Conn
	read *atomic.Int64
}

func (c countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.read.Add(int64(n))
	return n, err
}

// Write delays each message sent to the server by REALDATA_RTT (e.g. 1ms), standing in for the
// network round trip to a remote database; loopback has none.
func (c countingConn) Write(b []byte) (int, error) {
	if d, _ := time.ParseDuration(os.Getenv("REALDATA_RTT")); d > 0 {
		time.Sleep(d)
	}
	return c.Conn.Write(b)
}

func (d countingDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialTimeout(network, addr, 0)
}

func (d countingDialer) DialTimeout(network, addr string, timeout time.Duration) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(context.Background(), network, addr)
	if err != nil {
		return nil, err
	}
	return countingConn{conn, d.read}, nil
}

// realManager resolves images like the google driver: the pool's default image, or the variant's.
type realManager struct{ defaults map[string]string }

func (m realManager) FullyQualifiedImage(_ context.Context, pool, _, image string) (string, error) {
	if image == "" {
		return m.defaults[pool], nil
	}
	return "cie-hosted-vm-paid-prod/global/images/" + image, nil
}

func (realManager) Destroy(context.Context, string, string, *types.Instance, *storage.CleanupType) error {
	return nil
}

// realPod is one runner pod: its own connection pool and its own scaler (and so its own cache).
type realPod struct {
	db      *sqlx.DB
	read    *atomic.Int64
	insts   store.InstanceStore
	outbox  store.OutboxStore
	scaler  *CapabilityScaler
	sem     chan struct{}
	catalog *capability.Catalog
}

func newRealPod(t *testing.T, dsn string, cat *capability.Catalog, mgr realManager, disabled []string) *realPod {
	t.Helper()
	read := &atomic.Int64{}
	connector, err := pq.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connector.Dialer(countingDialer{read})
	db := sqlx.NewDb(sql.OpenDB(connector), "postgres")
	insts, outbox := dbsql.NewInstanceStore(db), dbsql.NewOutboxStore(db)
	cfg := types.ScalerConfig{DisabledPools: disabled}
	return &realPod{
		db: db, read: read, insts: insts, outbox: outbox, sem: make(chan struct{}, realJobsPerPod), catalog: cat,
		scaler: NewCapabilityScaler(mgr, nil, insts, nil, outbox, cat, cfg, &metric.Metrics{}),
	}
}

// legacySlot is what capabilitySlot did before: list the whole pool, count the slot's setup jobs.
func legacySlot(ctx context.Context, p *realPod, mgr realManager, key *types.CapabilityKey, m *capability.Member) (free, used int, err error) {
	image := ""
	if m.Variant != nil {
		image = m.Variant.ImageName
	}
	fq, _ := mgr.FullyQualifiedImage(ctx, m.Pool, key.TenantID, image)
	instances, err := p.insts.List(ctx, m.Pool, nil)
	if err != nil {
		return 0, 0, err
	}
	for _, inst := range instances {
		tenantID, variantID := tenantOrDefault(inst.TenantID), variantOrDefault(inst.VariantID)
		if tenantID != key.TenantID || variantID != key.VariantID || inst.State == types.StateTerminating {
			continue
		}
		used++
		if fq != "" && inst.Image != fq {
			continue
		}
		if inst.State == types.StateCreated || inst.State == types.StateHibernating || inst.State == types.StateProvisioning {
			free++
		}
	}
	var pending int
	err = p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_jobs WHERE job_type = $1 AND pool_name = $2
AND COALESCE(NULLIF(job_params->>'tenant_id', ''), $3) = $4
AND COALESCE(NULLIF(job_params->>'variant_id', ''), $5) = $6`,
		types.OutboxJobTypeSetupInstance, m.Pool, types.DefaultTenantID, key.TenantID, types.DefaultVariantID, key.VariantID).Scan(&pending)
	return free, used + pending, err
}

type readMode string

const (
	modeLegacy    readMode = "before: List pool + count per slot"
	modeAggregate readMode = "aggregate only (no sharing)"
	modeShared    readMode = "after: aggregate + shared per pool"
)

// scaleGroupReads performs the reads of one group's scale job in the given mode.
func scaleGroupReads(ctx context.Context, p *realPod, mgr realManager, mode readMode, g *capability.Group, disabled []string) error {
	members := p.scaler.activeMembers(g)
	key := g.Key
	switch mode {
	case modeLegacy:
		for i := range members {
			if _, _, err := legacySlot(ctx, p, mgr, &key, &members[i]); err != nil {
				return err
			}
		}
	case modeAggregate:
		for i := range members {
			if _, err := p.scaler.capabilitySlot(ctx, &key, &members[i], p.scaler.loadPoolCapacity); err != nil {
				return err
			}
		}
	case modeShared:
		if _, err := p.scaler.capabilitySlots(ctx, &key, members); err != nil {
			return err
		}
	}
	_ = disabled
	return nil
}

type burstStats struct {
	wall        time.Duration
	p50, p95    time.Duration
	max         time.Duration
	statements  int64
	rows        int64
	dbExecMs    float64
	clientBytes int64
}

func TestRealDataCapabilityReads(t *testing.T) {
	dsn, poolPath := os.Getenv("REALDATA_PG_DSN"), os.Getenv("REALDATA_POOL_FILE")
	if dsn == "" || poolPath == "" {
		t.Skip("REALDATA_PG_DSN and REALDATA_POOL_FILE not set")
	}
	ctx := context.Background()
	raw, err := os.ReadFile(poolPath)
	if err != nil {
		t.Fatal(err)
	}
	poolYAML := string(raw)
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if yaml.Unmarshal(raw, &cm) == nil && cm.Data["pool"] != "" { // a ConfigMap holding the pool file
		poolYAML = cm.Data["pool"]
	}
	pf, err := config.Parse(strings.NewReader(poolYAML))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := capability.NewCatalog(pf, []string{"google", "amazon"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	disabled := strings.Split(realDisabledPools, ",")

	admin, err := database.ConnectSQL("postgres", dsn) // also applies the migrations
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	targets := NewCapabilityScaleTargets(cat, disabled).ScaleTargets()
	loadOutbox(t, admin, targets)

	defaults := map[string]string{}
	for _, g := range realProd1Mix {
		if _, ok := defaults[g.pool]; !ok || g.state == types.StateCreated {
			defaults[g.pool] = g.image
		}
	}
	mgr := realManager{defaults: defaults}

	pods := make([]*realPod, realPods)
	for i := range pods {
		pods[i] = newRealPod(t, dsn, cat, mgr, disabled)
	}
	defer func() {
		for _, p := range pods {
			_ = p.db.Close()
		}
	}()

	var groups []capability.Group
	slots := 0
	for _, g := range cat.Groups() {
		if n := len(pods[0].scaler.activeMembers(&g)); n > 0 {
			groups = append(groups, g)
			slots += n
		}
	}
	t.Logf("real pool file: %d pools, %d scale groups, %d (group,pool) slots per window; %d pods x %d concurrent jobs",
		len(cat.Pools()), len(groups), slots, realPods, realJobsPerPod)

	type scenario struct {
		name  string
		scale float64
	}
	for _, sc := range []scenario{
		{"prod1 as measured (213 instances)", 1},
		{"prod1 at its 2-day peak (284 live, x1.33)", 1.33},
		{"prod2-like peak (632 live, x3)", 3},
	} {
		rows := loadInstances(t, admin, sc.scale)
		t.Logf("")
		t.Logf("=== %s: %d instance rows ===", sc.name, rows)
		checkSameCounts(ctx, t, pods[0], mgr, groups)

		for _, mode := range []readMode{modeLegacy, modeAggregate, modeShared} {
			var all []burstStats
			for rep := 0; rep < realBurstRepeats; rep++ {
				for _, p := range pods { // a new window: nothing is shared with the previous one
					p.scaler.capacity = newCapacityCache(capacityCacheTTL)
				}
				all = append(all, runBurst(ctx, t, admin, pods, mgr, mode, groups, disabled))
			}
			sort.Slice(all, func(i, j int) bool { return all[i].wall < all[j].wall })
			m := all[len(all)/2]
			t.Logf("%-40s statements=%4d rows=%7d  db_exec=%7.0fms  bytes=%8.2fMB  burst=%7.0fms  job p50=%6.1fms p95=%6.1fms max=%6.1fms",
				string(mode), m.statements, m.rows, m.dbExecMs, float64(m.clientBytes)/1e6,
				ms(m.wall), ms(m.p50), ms(m.p95), ms(m.max))
		}
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// checkSameCounts asserts the legacy and current reads give identical free and used counts for
// every slot of every group, and reports how many slots actually have instances.
func checkSameCounts(ctx context.Context, t *testing.T, p *realPod, mgr realManager, groups []capability.Group) {
	t.Helper()
	p.scaler.capacity = newCapacityCache(capacityCacheTTL)
	checked, nonZero, mismatches := 0, 0, 0
	for gi := range groups {
		key := groups[gi].Key
		members := p.scaler.activeMembers(&groups[gi])
		for i := range members {
			lf, lu, err := legacySlot(ctx, p, mgr, &key, &members[i])
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.scaler.capabilitySlot(ctx, &key, &members[i], p.scaler.sharedPoolCapacity)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			if lf+lu > 0 {
				nonZero++
			}
			if lf != got.free || lu != got.used {
				mismatches++
				t.Errorf("%s pool %s: legacy free/used = %d/%d, current = %d/%d", key.String(), members[i].Pool, lf, lu, got.free, got.used)
			}
		}
	}
	t.Logf("same counts as before: %d slots compared, %d with instances or pending jobs, %d mismatches", checked, nonZero, mismatches)
}

func runBurst(ctx context.Context, t *testing.T, admin *sqlx.DB, pods []*realPod, mgr realManager, mode readMode, groups []capability.Group, disabled []string) burstStats {
	t.Helper()
	if _, err := admin.ExecContext(ctx, "SELECT pg_stat_statements_reset()"); err != nil {
		t.Fatal(err)
	}
	var before int64
	for _, p := range pods {
		before += p.read.Load()
	}

	lat := make([]time.Duration, len(groups))
	var wg sync.WaitGroup
	start := time.Now()
	for i := range groups {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := pods[i%len(pods)] // jobs are claimed by whichever pod polls; spread evenly
			p.sem <- struct{}{}
			defer func() { <-p.sem }()
			t0 := time.Now()
			if err := scaleGroupReads(ctx, p, mgr, mode, &groups[i], disabled); err != nil {
				t.Errorf("group %s: %v", groups[i].Key.String(), err)
			}
			lat[i] = time.Since(t0)
		}(i)
	}
	wg.Wait()
	wall := time.Since(start)

	var after int64
	for _, p := range pods {
		after += p.read.Load()
	}
	var st burstStats
	st.wall, st.clientBytes = wall, after-before
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	st.p50, st.p95, st.max = lat[len(lat)/2], lat[int(math.Ceil(float64(len(lat))*0.95))-1], lat[len(lat)-1]

	row := admin.QueryRowContext(ctx, `SELECT COALESCE(SUM(calls),0), COALESCE(SUM(rows),0), COALESCE(SUM(total_exec_time),0)
FROM pg_stat_statements
WHERE (query ILIKE '%FROM instances%' OR query ILIKE '%FROM outbox_jobs%') AND query NOT ILIKE '%pg_stat_statements%'`)
	if err := row.Scan(&st.statements, &st.rows, &st.dbExecMs); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("REALDATA_VERBOSE") != "" {
		var rows []struct {
			Query string  `db:"query"`
			Calls int64   `db:"calls"`
			Rows  int64   `db:"rows"`
			Mean  float64 `db:"mean_exec_time"`
		}
		_ = admin.SelectContext(ctx, &rows, `SELECT left(regexp_replace(query, '\s+', ' ', 'g'), 110) AS query, calls, rows, mean_exec_time FROM pg_stat_statements
WHERE (query ILIKE '%FROM instances%' OR query ILIKE '%FROM outbox_jobs%') AND query NOT ILIKE '%pg_stat_statements%' ORDER BY calls DESC`)
		for _, r := range rows {
			t.Logf("   [%s] calls=%d rows=%d mean=%.3fms  %s", mode, r.Calls, r.Rows, r.Mean, r.Query)
		}
	}
	return st
}

func loadOutbox(t *testing.T, db *sqlx.DB, targets []string) {
	t.Helper()
	if _, err := db.ExecContext(ctx0, "TRUNCATE outbox_jobs"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	first := now - int64(realScaleWindows*300)
	first -= first % 300
	// The outbox keeps scale jobs for 48 hours; this is that history for every target.
	if _, err := db.ExecContext(ctx0, `INSERT INTO outbox_jobs (pool_name, runner_name, job_type, job_params, created_at, processed_at, status, retry_count)
SELECT t, '', 'scale', jsonb_build_object('window_start', ws, 'window_end', ws + 300), ws - 171, ws - 141, 'running', 1
FROM unnest($1::text[]) AS t, generate_series($2::bigint, $2::bigint + $3::bigint * 300, 300) AS ws`,
		pq.Array(targets), first, int64(realScaleWindows-1)); err != nil {
		t.Fatal(err)
	}
	// The setup jobs found on prod1 at the same time: 2 in linux-amd64, 1 in its fallback.
	ob := dbsql.NewOutboxStore(db)
	for _, pool := range []string{"linux-amd64", "linux-amd64", "linux-amd64-fallback"} {
		params, _ := json.Marshal(types.SetupInstanceParams{VariantID: "variant_medium"})
		raw := json.RawMessage(params)
		if err := ob.Create(ctx0, &types.OutboxJob{PoolName: pool, JobType: types.OutboxJobTypeSetupInstance, JobParams: &raw, Status: types.OutboxJobStatusPending}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx0, "ANALYZE outbox_jobs"); err != nil {
		t.Fatal(err)
	}
}

var ctx0 = context.Background()

// loadInstances fills the instances table with the prod1 mix scaled by factor. Rows carry
// random key and certificate material of the real lengths, so they are as wide as prod's.
func loadInstances(t *testing.T, db *sqlx.DB, factor float64) int {
	t.Helper()
	if _, err := db.ExecContext(ctx0, "TRUNCATE instances"); err != nil {
		t.Fatal(err)
	}
	is := dbsql.NewInstanceStore(db)
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // fixed seed: reproducible test data, not security-sensitive
	blob := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('A' + rng.Intn(26))
		}
		return b
	}
	total := 0
	for _, g := range realProd1Mix {
		n := int(math.Ceil(float64(g.n) * factor))
		for i := 0; i < n; i++ {
			total++
			tenant, variant := "", g.variant // stored empty for the default tenant, like old rows
			if g.variant == "default" {
				variant = ""
			}
			inst := &types.Instance{
				ID: fmt.Sprintf("i-%06d", total), Name: fmt.Sprintf("n-%06d", total), Provider: types.Google,
				State: g.state, Pool: g.pool, Image: g.image, VariantID: variant, TenantID: tenant,
				Address: "10.0.0.1", Zone: "us-west1-a", Size: "c4d-standard-8", Started: time.Now().Unix() - int64(rng.Intn(3600)),
				CAKey: blob(realSecretBytesCA), CACert: blob(realSecretBytesCrt), TLSKey: blob(realSecretBytesCA), TLSCert: blob(realSecretBytesCrt),
				Labels: []byte("{}"), Platform: types.Platform{OS: "linux", Arch: "amd64"}, Source: types.InstanceSourcePredictor,
			}
			if err := is.Create(ctx0, inst); err != nil {
				t.Fatalf("create instance: %v", err)
			}
		}
	}
	if _, err := db.ExecContext(ctx0, "ANALYZE instances"); err != nil {
		t.Fatal(err)
	}
	return total
}
