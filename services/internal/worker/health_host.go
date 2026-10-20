package worker

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type HealthChecker interface {
	Check(context.Context, HealthPlan) (HealthResult, error)
}
type RuntimeHealthChecker struct{ executor *Executor }

func NewRuntimeHealthChecker(executor *Executor) *RuntimeHealthChecker {
	return &RuntimeHealthChecker{executor}
}
func initialHealth() HealthResult {
	return HealthResult{Checks: map[instances.Dimension]HealthObservation{
		instances.Application:   {Status: instances.Unhealthy, Reason: "APPLICATION_UNAVAILABLE"},
		instances.Storage:       {Status: instances.Unhealthy, Reason: "STORAGE_MISSING"},
		instances.Configuration: {Status: instances.Unhealthy, Reason: "CONFIGURATION_INVALID"},
		instances.Access:        {Status: instances.Unhealthy, Reason: "ACCESS_UNAVAILABLE"},
	}}
}
func (r *RuntimeHealthChecker) Check(ctx context.Context, p HealthPlan) (HealthResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h := r.executor.host
	guard, cancelGuard := context.WithTimeout(ctx, 100*time.Millisecond)
	release, err := h.Acquire(guard, p.InstanceID)
	cancelGuard()
	if err != nil {
		return HealthResult{}, err
	}
	defer release()
	if err = h.EnsureHelpersAbsent(ctx, p.InstanceID); err != nil {
		return HealthResult{}, err
	}
	result := initialHealth()
	container, err := h.container(ctx, p.InstanceID)
	if err != nil || container == nil || validateContainer(*container, p.ExecutionPlan, h.options.RuntimeRoot, false) != nil || !container.State.Running || container.NetworkSettings.Networks["hc-"+p.InstanceID].IPAddress != p.IP || p.Host != h.host(p.InstanceID) {
		return result, nil
	}
	// Workspace authority and immutable build parameters are part of configuration
	// health. A suspended/foreign Owner must not remain a healthy runtime.
	workspaceContext, cancelWorkspace := context.WithTimeout(ctx, 2*time.Second)
	authority := r.executor.verifyOwner(workspaceContext, p.ExecutionPlan)
	workspace, deleted, workspaceErr := r.executor.lookup(workspaceContext, p.ExecutionPlan, false)
	cancelWorkspace()
	workspaceValid := authority == nil && workspaceErr == nil && !deleted && p.BuildID != nil && workspace.LatestBuild.ID == *p.BuildID && workspace.LatestBuild.Transition == "start" && workspace.LatestBuild.Job.Status == "succeeded"
	if !workspaceValid {
		result.Checks[instances.Configuration] = HealthObservation{Status: instances.Unhealthy, Reason: "WORKSPACE_UNAVAILABLE"}
	}
	native, configurationErr := h.healthConfiguration(p.ExecutionPlan)
	configurationValid := configurationErr == nil && workspaceValid
	if configurationValid {
		result.Checks[instances.Configuration] = HealthObservation{Status: instances.Healthy}
	}
	proof, storageErr := h.inspectStorage(ctx, p.ExecutionPlan, "inspect")
	if storageErr == nil && h.volume(ctx, p.ExecutionPlan, proof, false) == nil && h.storageReadWriteProbe(ctx, p.InstanceID) == nil {
		result.Checks[instances.Storage] = HealthObservation{Status: instances.Healthy}
	}
	isolated := h.network(ctx, p.InstanceID, "inspect") == nil
	cookie := p.Cookie
	publicHost := h.authority(p.InstanceID)
	if configurationValid && isolated && result.Checks[instances.Storage].Status == instances.Healthy && (!p.NativeExpiresAt.After(p.Started.Add(time.Minute)) || !validNativeHeader(publicHost, cookie)) {
		renewed, expiry, err := exchangeNative(ctx, "http://"+p.IP+":3080", publicHost, native.Token)
		if err == nil {
			result.cookie = renewed
			result.expires = expiry
			cookie = renewed
		}
	}
	native.Token = ""
	app, access := checkNativeHealth(ctx, "http://"+p.IP+":3080", publicHost, cookie)
	if app {
		result.Checks[instances.Application] = HealthObservation{Status: instances.Healthy}
	}
	if access && isolated && configurationValid {
		result.Checks[instances.Access] = HealthObservation{Status: instances.Healthy}
	}
	if ctx.Err() != nil {
		for d, v := range result.Checks {
			if v.Status != instances.Healthy {
				result.Checks[d] = HealthObservation{Status: instances.Unhealthy, Reason: "HEALTH_TIMEOUT"}
			}
		}
	}
	return result, nil
}
func (h *LocalHost) healthConfiguration(p ExecutionPlan) (nativeChannel, error) {
	channel := filepath.Join(h.options.RuntimeRoot, p.InstanceID)
	if privateDirectory(channel) != nil {
		return nativeChannel{}, ErrHostIdentity
	}
	desired, err := h.runtimeConfiguration(p)
	if err != nil {
		return nativeChannel{}, err
	}
	raw, err := config.ReadPrivateFile(filepath.Join(channel, "config.json"))
	if err != nil {
		return nativeChannel{}, err
	}
	var actual runtimeConfiguration
	if webapi.StrictUnmarshal(raw, &actual) != nil || !reflect.DeepEqual(actual, desired) {
		return nativeChannel{}, ErrHostIdentity
	}
	key, err := config.ReadPrivateFile(filepath.Join(channel, "model-key"))
	if err != nil {
		return nativeChannel{}, err
	}
	defer clear(key)
	if !bytes.Equal(key, p.ModelKey()) {
		return nativeChannel{}, ErrHostIdentity
	}
	raw, err = config.ReadPrivateFile(filepath.Join(channel, "native.json"))
	if err != nil {
		return nativeChannel{}, err
	}
	defer clear(raw)
	var native nativeChannel
	if webapi.StrictUnmarshal(raw, &native) != nil || !native.matches(p) {
		return nativeChannel{}, ErrHostIdentity
	}
	return native, nil
}

// The probe opens a no-follow directory descriptor, then creates a unique file
// through that descriptor. It reads/removes only its own O_EXCL inode and never
// changes workspace content or initializes a missing asset.
const storageHealthScript = `const f=require('fs'),c=f.constants;const root='/data/.hc-health';let dir,fd,path;
try{try{f.mkdirSync(root,{mode:0o700})}catch(e){if(e.code!=='EEXIST')throw e}
dir=f.openSync(root,c.O_RDONLY|c.O_DIRECTORY|c.O_NOFOLLOW);const d=f.fstatSync(dir);if(d.uid!==1000||(d.mode&0o777)!==0o700)throw Error('probe');
path='/proc/self/fd/'+dir+'/'+process.argv[1];fd=f.openSync(path,c.O_CREAT|c.O_EXCL|c.O_RDWR|c.O_NOFOLLOW,0o600);f.writeSync(fd,Buffer.from('hc-storage-health'));f.fsyncSync(fd);const b=Buffer.alloc(17);if(f.readSync(fd,b,0,b.length,0)!==17||b.toString()!=='hc-storage-health')throw Error('probe');process.stdout.write('ok');
}finally{if(fd!==undefined){const a=f.fstatSync(fd);const b=f.lstatSync(path);if(a.ino===b.ino&&a.dev===b.dev)f.unlinkSync(path);f.closeSync(fd)}if(dir!==undefined)f.closeSync(dir)}`

func (h *LocalHost) storageReadWriteProbe(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrHostIdentity
	}
	raw, err := dockerCommand(ctx, nil, "exec", "hc-"+id, "node", "-e", storageHealthScript, webapi.ID())
	if err != nil || string(raw) != "ok" {
		return errors.New("original storage read/write probe failed")
	}
	return nil
}
