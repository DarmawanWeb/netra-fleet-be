-- Robot load in the telemetry history (CPU, GPU, RAM, temperature) for the robot page charts.
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS cpu_percent DOUBLE PRECISION;
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS gpu_percent DOUBLE PRECISION;
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS memory_percent DOUBLE PRECISION;
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS memory_used_gb DOUBLE PRECISION;
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS memory_total_gb DOUBLE PRECISION;
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS cpu_temp_c DOUBLE PRECISION;
