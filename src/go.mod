module github.com/quvox/task_organizer

replace (
	github.com/quvox/task_organizer/internal/create => ./src/internal/create
	github.com/quvox/task_organizer/internal/master => ./src/internal/master
	github.com/quvox/task_organizer/internal/worker => ./src/internal/worker
	github.com/quvox/task_organizer/internal/common => ./src/internal/common
)

go 1.22.4
