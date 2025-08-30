package app

import (
	"context"
)

func (a *App) PrepareDB(ctx context.Context) error {
	const QUERY = `
			CREATE TABLE IF NOT EXISTS tasks (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				pid TEXT UNIQUE NOT NULL,
				command TEXT NOT NULL,
				priority INTEGER DEFAULT 0,
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

func (a *App) InsertTask(ctx context.Context, task Task) error {
	const QUERY = `
			INSERT into tasks (pid, command, priority, status) VALUES (?, ?, ?, 'pending')
		`
	if _, err := a.db.ExecContext(ctx, QUERY, task.PID, task.Command, task.Priority); err != nil {
		return err
	}

	return nil
}

func (a *App) UpdateTask(ctx context.Context, task Task) error {
	const QUERY = `
			UPDATE tasks
			SET status = ?, output = ?, error = ?, exit_code = ?, updated_at = CURRENT_TIMESTAMP
			WHERE pid = ?
		`
	if _, err := a.db.ExecContext(
		ctx,
		QUERY,
		task.Status,
		task.Output,
		task.Error,
		task.ExitCode,
		task.PID,
	); err != nil {
		return err
	}

	return nil
}

func (a *App) GetAllTasks(ctx context.Context) ([]Task, error) {
	const QUERY = `
			SELECT
				id,
				pid,
				command,
				status,
				priority,
				COALESCE (output, '') as output,
				COALESCE (error, '') as error,
				COALESCE (exit_code, 0) as exit_code,
				created_at,
				updated_at
			FROM tasks
			ORDER BY created_at DESC, id DESC
		`
	rows, err := a.db.QueryContext(ctx, QUERY)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var cmd Task
		err := rows.Scan(
			&cmd.ID,
			&cmd.PID,
			&cmd.Command,
			&cmd.Status,
			&cmd.Priority,
			&cmd.Output,
			&cmd.Error,
			&cmd.ExitCode,
			&cmd.CreatedAt,
			&cmd.UpdatedAt,
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

func (a *App) GetPendingTask(ctx context.Context) (*Task, error) {
	const QUERY = `
			SELECT pid, command FROM tasks
			WHERE status = 'pending'
			ORDER BY priority DESC, id ASC
			LIMIT 1
	`
	var task Task

	err := a.db.QueryRowContext(ctx, QUERY).Scan(&task.PID, &task.Command)
	if err != nil {
		return nil, err
	}

	return &task, err
}
