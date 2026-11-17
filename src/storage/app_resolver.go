package storage

import (
	"SignTools/src/util"
	"fmt"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// NewInstallOnly keeps an already signed or DRM-protected IPA byte-for-byte.
// It creates an installation record without profiles, signing jobs or builders.
func (r *appResolver) NewInstallOnly(source io.Reader, name string) (App, error) {
	a := newApp(uuid.NewString())
	if err := a.MkDir(AppRoot); err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = a.delete()
		}
	}()
	if err := a.SetFile(AppUnsignedFile, source); err != nil {
		return nil, err
	}
	f, err := a.GetFile(AppUnsignedFile)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	info, err := util.ReadAppMetadata(f, stat.Size())
	if err != nil || info.BundleID == "" {
		_ = f.Close()
		return nil, fmt.Errorf("install-only import requires an IPA with a valid main-bundle Info.plist")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	err = a.SetFile(AppSignedFile, f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	for field, value := range map[FSName]string{AppName: name, AppInstallOnly: "true", AppBuilderId: "", AppProfileId: "", AppWorkflowUrl: "", AppBundleId: info.BundleID} {
		if err := a.SetString(field, value); err != nil {
			return nil, err
		}
	}
	_ = a.SaveInfo() // Missing artwork does not invalidate installation metadata.
	if err := a.CaptureSignedInfo(); err != nil {
		return nil, err
	}
	a.watch()
	r.mutex.Lock()
	r.idToAppMap[a.GetId()] = a
	r.mutex.Unlock()
	complete = true
	Changes.notify()
	return a, nil
}

func newAppResolver() *appResolver {
	return &appResolver{
		idToAppMap: map[string]App{},
	}
}

type appResolver struct {
	idToAppMap map[string]App
	mutex      sync.Mutex
}

func (r *appResolver) refresh() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	idDirs, err := os.ReadDir(appsPath)
	if err != nil {
		return errors.WithMessage(err, "read apps dir")
	}
	idDirs = util.RemoveHiddenDirs(idDirs)
	for _, idDir := range idDirs {
		id := idDir.Name()
		r.idToAppMap[id] = loadApp(id)
	}
	return nil
}

func (r *appResolver) GetAll() ([]App, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var apps []App
	for _, app := range r.idToAppMap {
		apps = append(apps, app)
	}
	modTimes := make(map[string]time.Time, len(apps))
	for _, app := range apps {
		modTimes[app.GetId()], _ = app.GetModTime()
	}
	// newest first; the id breaks ties, since apps come from a map in random order and
	// e.g. restored data has equal times, which would reorder cards between renders
	sort.Slice(apps, func(i, j int) bool {
		time1, time2 := modTimes[apps[i].GetId()], modTimes[apps[j].GetId()]
		if !time1.Equal(time2) {
			return time1.After(time2)
		}
		return apps[i].GetId() < apps[j].GetId()
	})
	return apps, nil
}

func (r *appResolver) Get(id string) (App, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	app, ok := r.idToAppMap[id]
	if !ok {
		return nil, false
	}
	return app, true
}

func (r *appResolver) New(unsignedFile io.Reader, name string, profile Profile, signArgs string, userBundleId string, builderId string, tweakMap map[string]io.Reader) (App, error) {
	app, err := createApp(unsignedFile, name, profile, signArgs, userBundleId, builderId, tweakMap)
	if err != nil {
		return nil, err
	}
	app.watch()
	r.mutex.Lock()
	r.idToAppMap[app.GetId()] = app
	r.mutex.Unlock()
	Changes.notify()
	return app, nil
}

func (r *appResolver) Delete(id string) error {
	r.mutex.Lock()
	app, ok := r.idToAppMap[id]
	if !ok {
		r.mutex.Unlock()
		return nil
	}
	appId := app.GetId()
	delete(r.idToAppMap, appId)
	r.mutex.Unlock()
	Changes.notify()
	if err := app.delete(); err != nil {
		return errors.WithMessagef(err, "delete app id=%s", appId)
	}
	return nil
}
