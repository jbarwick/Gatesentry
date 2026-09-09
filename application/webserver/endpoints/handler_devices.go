package gatesentryWebserverEndpoints

import (
	"encoding/json"
	"log"
	"net/http"

	"bitbucket.org/abdullah_irfan/gatesentryf/dns/discovery"
	gatesentryDnsServer "bitbucket.org/abdullah_irfan/gatesentryf/dns/server"
	"github.com/gorilla/mux"
)

// deviceStoreOrError returns the device store or writes a 503 error.
func deviceStoreOrError(w http.ResponseWriter) *discovery.DeviceStore {
	ds := gatesentryDnsServer.GetDeviceStore()
	if ds == nil {
		http.Error(w, `{"error":"Device store not initialized — DNS server may not be running"}`, http.StatusServiceUnavailable)
		return nil
	}
	return ds
}

func writeDevicesJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// GSApiDevicesGetAll returns all devices in the inventory.
// GET /api/devices
func GSApiDevicesGetAll(w http.ResponseWriter, _ *http.Request) {
	ds := deviceStoreOrError(w)
	if ds == nil {
		return
	}

	devices := ds.GetAllDevices()
	writeDevicesJSON(w, http.StatusOK, map[string]interface{}{
		"devices": devices,
		"count":   len(devices),
	})
}

// GSApiDevicesProbe pings all devices and returns the updated inventory.
// POST /api/devices/probe
func GSApiDevicesProbe(w http.ResponseWriter, r *http.Request) {
	ds := deviceStoreOrError(w)
	if ds == nil {
		return
	}

	summary := ds.ProbeReachability(r.Context())
	devices := ds.GetAllDevices()
	writeDevicesJSON(w, http.StatusOK, map[string]interface{}{
		"devices":        devices,
		"count":          len(devices),
		"ping_available": summary.Available,
		"probed":         summary.Probed,
		"online":         summary.Online,
	})
}

// GSApiDeviceProbe pings a single device and returns it.
// POST /api/devices/{id}/probe
func GSApiDeviceProbe(w http.ResponseWriter, r *http.Request) {
	ds := deviceStoreOrError(w)
	if ds == nil {
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]

	device := ds.ProbeDevice(r.Context(), id)
	if device == nil {
		writeDevicesJSON(w, http.StatusNotFound, map[string]string{"error": "Device not found"})
		return
	}

	writeDevicesJSON(w, http.StatusOK, map[string]interface{}{
		"device":         device,
		"ping_available": discovery.PingSupported(),
	})
}

// GSApiDeviceGet returns a single device by ID.
// GET /api/devices/{id}
func GSApiDeviceGet(w http.ResponseWriter, r *http.Request) {
	ds := deviceStoreOrError(w)
	if ds == nil {
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]

	device := ds.GetDevice(id)
	if device == nil {
		http.Error(w, `{"error":"Device not found"}`, http.StatusNotFound)
		return
	}

	writeDevicesJSON(w, http.StatusOK, map[string]interface{}{
		"device": device,
	})
}

// nameRequest is the JSON body for naming/updating a device.
type nameRequest struct {
	Name     string `json:"name"`
	Owner    string `json:"owner,omitempty"`
	Category string `json:"category,omitempty"`
}

// GSApiDeviceSetName sets the manual name (and optionally owner/category) for a device.
// POST /api/devices/{id}/name
func GSApiDeviceSetName(w http.ResponseWriter, r *http.Request) {
	ds := deviceStoreOrError(w)
	if ds == nil {
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]

	device := ds.GetDevice(id)
	if device == nil {
		http.Error(w, `{"error":"Device not found"}`, http.StatusNotFound)
		return
	}

	var req nameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	// Update the device via UpsertDevice to trigger DNS record rebuild
	device.ManualName = req.Name
	if req.Owner != "" {
		device.Owner = req.Owner
	}
	if req.Category != "" {
		device.Category = req.Category
	}
	device.Persistent = true // Named devices should survive restarts

	ds.UpsertDevice(device)

	log.Printf("[Devices API] Device %s named: %q (owner=%q, category=%q)", id, req.Name, req.Owner, req.Category)

	// Return updated device
	updated := ds.GetDevice(id)
	writeDevicesJSON(w, http.StatusOK, map[string]interface{}{
		"device": updated,
	})
}

// GSApiDeviceDelete removes a device from the inventory.
// DELETE /api/devices/{id}
func GSApiDeviceDelete(w http.ResponseWriter, r *http.Request) {
	ds := deviceStoreOrError(w)
	if ds == nil {
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]

	device := ds.GetDevice(id)
	if device == nil {
		http.Error(w, `{"error":"Device not found"}`, http.StatusNotFound)
		return
	}

	ds.RemoveDevice(id)

	log.Printf("[Devices API] Device %s (%s) removed", id, device.GetDisplayName())

	writeDevicesJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Device removed",
	})
}
