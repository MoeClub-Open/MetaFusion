package assistant

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func (h HTTP) engine() *MissionEngine {
	if h.Missions != nil {
		return h.Missions
	}
	if h.Store != nil {
		return h.Store.EnsureMissionEngine(h.Catalog)
	}
	return nil
}

func (h HTTP) registerMissions(self *gin.RouterGroup, admin *gin.RouterGroup) {
	self.GET("/missions", h.listMissions(false))
	self.POST("/missions", h.createMission)
	self.GET("/missions/:id", h.getMission(false))
	self.DELETE("/missions/:id", h.deleteMission)
	self.GET("/missions/:id/tasks/:task_id", h.getMissionTask)
	self.GET("/missions/:id/planner-runs/:run_id", h.getMissionPlannerRun)
	self.POST("/missions/:id/steer", h.steerMission)
	self.POST("/missions/:id/pause", h.changeMissionStatus("paused", false))
	self.POST("/missions/:id/resume", h.changeMissionStatus("running", false))
	self.POST("/missions/:id/stop", h.changeMissionStatus("stopped", false))

	admin.GET("/missions", h.listMissions(true))
	admin.GET("/missions/:id", h.getMission(true))
	admin.POST("/missions/:id/stop", h.changeMissionStatus("stopped", true))
}

func (h HTTP) listMissions(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		l, o := pageArgs(c)
		status := c.Query("status")
		if status != "" && status != "running" && status != "paused" && status != "completed" && status != "stopped" && status != "failed" {
			writeResponse(c, nil, errors.New("assistant_invalid_status"))
			return
		}
		f := MissionFilter{Status: status}
		if admin {
			f.OwnerID = c.Query("user_id")
			f.Scope = c.Query("scope")
			if f.Scope != "" && f.Scope != "user" && f.Scope != "curator" {
				writeResponse(c, nil, errors.New("assistant_invalid_mission_scope"))
				return
			}
		} else {
			f.Scope = "user"
			f.OwnerID = catalog.RequestUser(c).ID
		}
		v, err := h.Store.Missions(c.Request.Context(), f, admin, l, o)
		writeResponse(c, v, err)
	}
}

func (h HTTP) createMission(c *gin.Context) {
	var in MissionCreateRequest
	if !decodeBody(c, &in) {
		return
	}
	u := *catalog.RequestUser(c)
	detail, err := h.Store.CreateMission(c.Request.Context(), "user", u.ID, u.ID, ActorFromCatalogUser(u), in)
	if err == nil {
		if eng := h.engine(); eng != nil && detail.Mission.Status == "running" {
			eng.Wake(detail.Mission.ID)
		}
	}
	writeResponse(c, detail, err)
}

func (h HTTP) getMission(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := ""
		if !admin {
			owner = catalog.RequestUser(c).ID
		}
		v, err := h.Store.GetMissionDetail(c.Request.Context(), c.Param("id"), owner, admin)
		if err == nil && !admin && v.Mission.Scope != "user" {
			err = ErrNotFound
		}
		writeResponse(c, v, err)
	}
}

func (h HTTP) getMissionTask(c *gin.Context) {
	owner := catalog.RequestUser(c).ID
	detail, err := h.Store.getMission(c.Request.Context(), c.Param("id"), owner, false)
	if err == nil && detail.Scope != "user" {
		err = ErrNotFound
	}
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	v, err := h.Store.GetMissionTask(c.Request.Context(), c.Param("id"), c.Param("task_id"), owner, false)
	writeResponse(c, v, err)
}

func (h HTTP) getMissionPlannerRun(c *gin.Context) {
	owner := catalog.RequestUser(c).ID
	detail, err := h.Store.getMission(c.Request.Context(), c.Param("id"), owner, false)
	if err == nil && detail.Scope != "user" {
		err = ErrNotFound
	}
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	v, err := h.Store.GetMissionPlannerRun(c.Request.Context(), c.Param("id"), c.Param("run_id"), owner, false)
	writeResponse(c, v, err)
}

func (h HTTP) steerMission(c *gin.Context) {
	var in MissionSteerRequest
	if !decodeBody(c, &in) {
		return
	}
	u := *catalog.RequestUser(c)
	m, err := h.Store.getMission(c.Request.Context(), c.Param("id"), u.ID, false)
	if err == nil && m.Scope != "user" {
		err = ErrNotFound
	}
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	actor := ActorFromCatalogUser(u)
	updated, err := h.Store.SteerMission(c.Request.Context(), m.ID, u.ID, false, &actor, in)
	if err == nil && updated.Status == "running" {
		if eng := h.engine(); eng != nil {
			eng.Wake(updated.ID)
		}
	}
	writeResponse(c, updated, err)
}

func (h HTTP) changeMissionStatus(status string, admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := *catalog.RequestUser(c)
		owner := u.ID
		if admin {
			owner = ""
		} else {
			m, err := h.Store.getMission(c.Request.Context(), c.Param("id"), owner, false)
			if err == nil && m.Scope != "user" {
				err = ErrNotFound
			}
			if err != nil {
				writeResponse(c, nil, err)
				return
			}
		}
		actor := ActorFromCatalogUser(u)
		updated, err := h.Store.SetMissionStatus(c.Request.Context(), c.Param("id"), owner, admin, status, &actor)
		if err == nil {
			if eng := h.engine(); eng != nil {
				if status == "running" {
					eng.Wake(updated.ID)
				} else {
					eng.Interrupt(updated.ID)
				}
			}
		}
		writeResponse(c, updated, err)
	}
}

func (h HTTP) deleteMission(c *gin.Context) {
	owner := catalog.RequestUser(c).ID
	m, err := h.Store.getMission(c.Request.Context(), c.Param("id"), owner, false)
	if err == nil && m.Scope != "user" {
		err = ErrNotFound
	}
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	if err := h.Store.DeleteMission(c.Request.Context(), m.ID, owner, false); err != nil {
		writeResponse(c, nil, err)
		return
	}
	c.Status(204)
}
