package app

import "context"

func (a *App) PrepareDB(ctx context.Context) error {
	const QUERY = `
			CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY,
				command TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				output TEXT,
				error TEXT,
				exit_code INTEGER,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`
	if _, err := a.db.ExecContext(ctx, QUERY); err != nil {
		return err
	}
	return nil
}

func (a *App) InsertTask(ctx context.Context, id, command string) error {
	const QUERY = `
			INSERT into tasks (id, command, status) VALUES (?, ?, 'pending')
		`
	if _, err := a.db.ExecContext(ctx, QUERY, id, command); err != nil {
		return err
	}

	return nil
}

func (a *App) UpdateTaskStatus(ctx context.Context, id, status, output, errorMsg string, exitCode int) error {
	const QUERY = `
			UPDATE tasks
			SET status = ?, output = ?, error = ?, exit_code = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`
	if _, err := a.db.ExecContext(
		ctx,
		QUERY,
		status,
		output,
		errorMsg,
		exitCode,
		id,
	); err != nil {
		return err
	}

	return nil
}

func (a *App) GetPendingTask(ctx context.Context) (id, command string, err error) {
	const QUERY = `
			SELECT id, command FROM tasks
			WHERE status = 'pending'
			ORDER BY created_at ASC
			LIMIT 1
		`

	err = a.db.QueryRowContext(ctx, QUERY).Scan(&id, &command)
	return id, command, err
}
