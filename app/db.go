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

func (a *App) GetAllTasks(ctx context.Context) ([]Command, error) {
	const QUERY = `
			SELECT
				id,
				command,
				status,
				COALESCE (output, '') as output,
				COALESCE (error, '') as error,
				COALESCE (exit_code, 0) as exit_code,
				created_at,
				updated_at
			FROM tasks
			ORDER BY created_at DESC
		`
	rows, err := a.db.QueryContext(ctx, QUERY)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Command
	for rows.Next() {
		var cmd Command
		err := rows.Scan(
			&cmd.ID,
			&cmd.Command,
			&cmd.Status,
			&cmd.Output,
			&cmd.Error,
			&cmd.ExitCode,
			&cmd.UpdatedAt,
			&cmd.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, cmd)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return tasks, nil
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
