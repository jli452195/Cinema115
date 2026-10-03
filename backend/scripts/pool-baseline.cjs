// Run from any directory; output contains only simulated data.
const { spawnSync } = require('node:child_process')
const fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '..')
const run = spawnSync('go', ['test', './internal/service', '-run', '^TestPoolSimulationBaseline$', '-count=1', '-json', '-timeout=120s'], { cwd: root, encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 })
if (run.error) throw run.error
if (run.status !== 0) { process.stderr.write(run.stdout + run.stderr); process.exit(run.status || 1) }
const scenarios = []
for (const line of run.stdout.split(/\r?\n/)) {
 if (!line) continue
 const event = JSON.parse(line), output = event.Output || '', marker = 'POOL_BASELINE '
 const i = output.indexOf(marker)
 if (i >= 0) scenarios.push(JSON.parse(output.slice(i + marker.length)))
}
if (scenarios.length !== 5) throw new Error('Missing baseline scenarios')
const report = { version: 1, environment: `${process.platform}/${process.arch}`, measuredAt: new Date().toISOString(), simulated: true, clientOperationDelayMs: 2, note: 'Independent cache per worker; fixed one film per worker. Counts are instrumented fake client operations, not physical HTTP requests. Transfer/rollback probes have zero delay. Zero queue/occupancy means that subsystem is outside the scenario. api_queue uses 1 ms intervals. Latency includes local storage and scheduling.', scenarios }
const output = process.argv[2]
if (output) fs.writeFileSync(path.resolve(output), JSON.stringify(report, null, 2) + '\n')
process.stdout.write(JSON.stringify(report, null, 2) + '\n')
