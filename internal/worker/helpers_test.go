package worker

import (
	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"gorm.io/gorm"
)

// newTestWorker builds a worker with every analysis kind and derivative kind
// enabled, mirroring the default compiled-in module set.
func newTestWorker(svc *service.FileService, db *gorm.DB, tmp string) *Worker {
	w := New(nil, svc, &database.DB{DB: db}, tmp)
	installTestRegistries(w, svc)
	return w
}

func newTestWorkerWithBus(bus *eventbus.Bus, svc *service.FileService, db *gorm.DB, tmp string) *Worker {
	w := New(bus, svc, &database.DB{DB: db}, tmp)
	installTestRegistries(w, svc)
	return w
}

// installTestRegistries enables the image and video modules on the worker.
func installTestRegistries(w *Worker, svc *service.FileService) {
	analysis := core.NewAnalysisRegistry(svc)
	analysis.Enable("image")
	analysis.Enable("video")
	analysis.Enable("audio")
	w.SetAnalysis(analysis)

	derivatives := core.NewDerivativeRegistry()
	derivatives.Register("image", w.ProcessImage)
	derivatives.Register("video", w.ProcessVideo)
	w.SetDerivatives(derivatives)
}
