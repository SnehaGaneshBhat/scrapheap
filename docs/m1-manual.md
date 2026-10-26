# Scrap Heap M1 manual acceptance run

This checklist exercises a real coordinator and worker processes over HTTP. The worker command is a placeholder until P4's native worker is available.

## Steps

1. From the repository root, start the coordinator with 200 burn tasks:

   ```sh
   go run ./cmd/server -job burn -burn-tasks 200 -burn-iters 50000000
   ```

   To start the job as soon as the server is ready, add `-autostart`. Otherwise, start it in step 3.

2. In two separate terminals, start workers A and B (once `cmd/worker` is ready):

   ```sh
   go run ./cmd/worker -server <ip>:8080 -name A
   go run ./cmd/worker -server <ip>:8080 -name B
   ```

3. Start the job from another terminal, or use the `-autostart` option from step 1:

   ```sh
   curl -X POST http://localhost:8080/api/job/start
   ```

4. Watch the job snapshot. Once work is underway, stop worker A with Ctrl+C or unplug its laptop. Its leased task should expire, its worker status should become `lost`, and its task should be requeued.

   Linux/macOS:

   ```sh
   watch -n1 'curl -s http://localhost:8080/api/job'
   ```

   Windows PowerShell:

   ```powershell
   while ($true) {
       Invoke-RestMethod http://localhost:8080/api/job | ConvertTo-Json -Depth 10
       Start-Sleep -Seconds 1
   }
   ```

5. While the job is running, start a new worker with a new name, then restart worker A using its original name:

   ```sh
   go run ./cmd/worker -server <ip>:8080 -name C
   go run ./cmd/worker -server <ip>:8080 -name A
   ```

6. Keep watching until the job is complete.

## Pass criteria

- `/api/job` reports `done == total == 200` and `state == "done"`.
- `requeued > 0`, showing that work from the stopped worker was recovered.
- Worker A appears as `lost`, then as `rejoined` or `active` after restarting with the same name.
- Worker C appears in the worker list and completes at least one task.
