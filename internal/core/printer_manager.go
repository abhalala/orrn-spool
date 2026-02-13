package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/orrn/spool/internal/config"
	"github.com/orrn/spool/internal/db"
)

var (
	ErrPrinterNotFound      = errors.New("printer not found")
	ErrPrinterOffline       = errors.New("printer is offline")
	ErrConnectionFailed     = errors.New("connection failed")
	ErrTimeout              = errors.New("operation timed out")
	ErrInvalidStatus        = errors.New("invalid status response")
	ErrPrinterCannotPrint   = errors.New("printer cannot print in current state")
	ErrPrinterAlreadyExists = errors.New("printer already exists")
)

const (
	defaultTCPPort         = 9100
	statusCommand          = "\x1b!?"
	statusResponseLength   = 4
	defaultReadWriteTimeout = 10 * time.Second
)

var printerStateMap = map[byte]string{
	'@': "normal",
	'F': "feeding",
	'P': "paused",
	'E': "error",
	'H': "head_open",
	'S': "standby",
	'L': "label_waiting",
	'I': "idle",
}

var warningMap = map[byte]string{
	'@': "none",
	'A': "paper_low",
	'B': "ribbon_low",
	'C': "paper_and_ribbon_low",
}

var errorMap = map[byte]string{
	'@': "none",
	'A': "head_overheat",
	'B': "motor_overheat",
	'C': "head_and_motor_overheat",
	'D': "head_error",
	'E': "cutter_error",
	'F': "rtc_error",
}

var mediaErrorMap = map[byte]string{
	'@': "none",
	'A': "paper_empty",
	'B': "ribbon_empty",
	'C': "paper_and_ribbon_empty",
	'D': "takeup_reel_full",
	'`': "head_open",
}

type PrinterManager struct {
	db            *sql.DB
	config        *config.PrintersConfig
	printers      map[int64]*Printer
	connections   map[int64]net.Conn
	mu            sync.RWMutex
	webhookSender WebhookSender
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

func NewPrinterManager(database *sql.DB, cfg *config.PrintersConfig, webhookSender WebhookSender) *PrinterManager {
	return &PrinterManager{
		db:            database,
		config:        cfg,
		printers:      make(map[int64]*Printer),
		connections:   make(map[int64]net.Conn),
		webhookSender: webhookSender,
		stopCh:        make(chan struct{}),
	}
}

func (pm *PrinterManager) Start() {
	pm.loadPrintersFromDB()
	
	pm.wg.Add(1)
	go pm.healthCheckLoop()
}

func (pm *PrinterManager) Stop() {
	close(pm.stopCh)
	
	pm.mu.Lock()
	for id, conn := range pm.connections {
		if conn != nil {
			conn.Close()
			delete(pm.connections, id)
		}
	}
	pm.mu.Unlock()
	
	pm.wg.Wait()
}

func (pm *PrinterManager) loadPrintersFromDB() {
	rows, err := pm.db.Query(db.ListPrinters)
	if err != nil {
		return
	}
	defer rows.Close()
	
	for rows.Next() {
		var p Printer
		var lastSeenAt sql.NullTime
		err := rows.Scan(
			&p.ID, &p.Name, &p.IPAddress, &p.Port, &p.DPI,
			&p.LabelWidthMM, &p.LabelHeightMM, &p.GapMM,
			&p.Status, &lastSeenAt, &p.TotalPrints,
			new(any), new(any),
		)
		if err != nil {
			continue
		}
		if lastSeenAt.Valid {
			p.LastSeenAt = &lastSeenAt.Time
		}
		pm.printers[p.ID] = &p
	}
}

func (pm *PrinterManager) AddPrinter(p *Printer) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if _, exists := pm.printers[p.ID]; exists {
		return ErrPrinterAlreadyExists
	}
	
	if p.Port == 0 {
		p.Port = defaultTCPPort
	}
	p.Status = "unknown"
	
	_, err := pm.db.Exec(db.InsertPrinter,
		p.Name, p.IPAddress, p.Port, p.DPI,
		p.LabelWidthMM, p.LabelHeightMM, p.GapMM, p.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to insert printer: %w", err)
	}
	
	pm.printers[p.ID] = p
	
	return nil
}

func (pm *PrinterManager) RemovePrinter(id int64) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if conn, exists := pm.connections[id]; exists {
		if conn != nil {
			conn.Close()
		}
		delete(pm.connections, id)
	}
	
	if _, exists := pm.printers[id]; !exists {
		return ErrPrinterNotFound
	}
	
	_, err := pm.db.Exec(db.DeletePrinter, id)
	if err != nil {
		return fmt.Errorf("failed to delete printer: %w", err)
	}
	
	delete(pm.printers, id)
	
	return nil
}

func (pm *PrinterManager) GetPrinter(id int64) (*Printer, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	p, exists := pm.printers[id]
	if !exists {
		return nil, ErrPrinterNotFound
	}
	
	return p, nil
}

func (pm *PrinterManager) ListPrinters() []*Printer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	printers := make([]*Printer, 0, len(pm.printers))
	for _, p := range pm.printers {
		printers = append(printers, p)
	}
	return printers
}

func (pm *PrinterManager) connect(id int64) (net.Conn, error) {
	pm.mu.RLock()
	p, exists := pm.printers[id]
	if !exists {
		pm.mu.RUnlock()
		return nil, ErrPrinterNotFound
	}
	
	if conn, exists := pm.connections[id]; exists && conn != nil {
		pm.mu.RUnlock()
		return conn, nil
	}
	pm.mu.RUnlock()
	
	address := fmt.Sprintf("%s:%d", p.IPAddress, p.Port)
	timeout := pm.config.ConnectionTimeout
	if timeout == 0 {
		timeout = defaultReadWriteTimeout
	}
	
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	
	pm.mu.Lock()
	pm.connections[id] = conn
	pm.mu.Unlock()
	
	return conn, nil
}

func (pm *PrinterManager) disconnect(id int64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if conn, exists := pm.connections[id]; exists {
		if conn != nil {
			conn.Close()
		}
		delete(pm.connections, id)
	}
}

func (pm *PrinterManager) reconnect(id int64) (net.Conn, error) {
	pm.disconnect(id)
	return pm.connect(id)
}

func (pm *PrinterManager) CheckStatus(id int64) (*PrinterStatus, error) {
	pm.mu.RLock()
	p, exists := pm.printers[id]
	if !exists {
		pm.mu.RUnlock()
		return nil, ErrPrinterNotFound
	}
	pm.mu.RUnlock()
	
	conn, err := pm.connect(id)
	if err != nil {
		status := &PrinterStatus{
			IsOnline:    false,
			CanPrint:    false,
			LastChecked: time.Now(),
		}
		pm.updatePrinterStatus(id, "offline")
		return status, err
	}
	
	timeout := pm.config.ConnectionTimeout
	if timeout == 0 {
		timeout = defaultReadWriteTimeout
	}
	
	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)
	
	_, err = conn.Write([]byte(statusCommand))
	if err != nil {
		conn, err = pm.reconnect(id)
		if err != nil {
			status := &PrinterStatus{
				IsOnline:    false,
				CanPrint:    false,
				LastChecked: time.Now(),
			}
			pm.updatePrinterStatus(id, "offline")
			return status, err
		}
		_ = conn.SetDeadline(deadline)
		_, err = conn.Write([]byte(statusCommand))
		if err != nil {
			pm.disconnect(id)
			status := &PrinterStatus{
				IsOnline:    false,
				CanPrint:    false,
				LastChecked: time.Now(),
			}
			pm.updatePrinterStatus(id, "offline")
			return status, err
		}
	}
	
	response := make([]byte, statusResponseLength)
	totalRead := 0
	for totalRead < statusResponseLength {
		n, err := conn.Read(response[totalRead:])
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			pm.disconnect(id)
			status := &PrinterStatus{
				IsOnline:    false,
				CanPrint:    false,
				LastChecked: time.Now(),
			}
			pm.updatePrinterStatus(id, "offline")
			return status, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
		}
		totalRead += n
	}
	
	if totalRead < statusResponseLength {
		status := &PrinterStatus{
			IsOnline:    false,
			CanPrint:    false,
			LastChecked: time.Now(),
		}
		pm.updatePrinterStatus(id, "error")
		return status, ErrInvalidStatus
	}
	
	status := pm.parseStatus(response)
	status.IsOnline = true
	status.LastChecked = time.Now()
	status.CanPrint = status.PrinterState == "normal" || status.PrinterState == "standby" || status.PrinterState == "idle"
	
	newStatus := pm.determineStatusString(status)
	pm.updatePrinterStatus(id, newStatus)
	
	return status, nil
}

func (pm *PrinterManager) parseStatus(response []byte) *PrinterStatus {
	status := &PrinterStatus{
		RawStatus: [4]byte{response[0], response[1], response[2], response[3]},
	}
	
	if state, ok := printerStateMap[response[0]]; ok {
		status.PrinterState = state
	} else {
		status.PrinterState = "unknown"
	}
	
	if warning, ok := warningMap[response[1]]; ok {
		status.Warning = warning
	} else {
		status.Warning = "unknown"
	}
	
	if err, ok := errorMap[response[2]]; ok {
		status.Error = err
	} else {
		status.Error = "unknown"
	}
	
	if mediaErr, ok := mediaErrorMap[response[3]]; ok {
		status.MediaError = mediaErr
	} else {
		status.MediaError = "unknown"
	}
	
	return status
}

func (pm *PrinterManager) determineStatusString(status *PrinterStatus) string {
	if !status.IsOnline {
		return "offline"
	}
	
	if status.PrinterState == "error" || status.Error != "none" {
		return "error"
	}
	
	if status.PrinterState == "paused" {
		return "paused"
	}
	
	if status.MediaError != "none" {
		return "error"
	}
	
	if status.PrinterState == "feeding" {
		return "busy"
	}
	
	return "online"
}

func (pm *PrinterManager) updatePrinterStatus(id int64, status string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	p, exists := pm.printers[id]
	if !exists {
		return
	}
	
	oldStatus := p.Status
	p.Status = status
	now := time.Now()
	p.LastSeenAt = &now
	
	_, _ = pm.db.Exec(db.UpdatePrinterStatus, status, id)
	
	if oldStatus != status && pm.webhookSender != nil {
		go pm.webhookSender.SendPrinterStatusChange(id, p.Name, oldStatus, status, nil)
	}
}

func (pm *PrinterManager) CheckAllStatuses() {
	pm.mu.RLock()
	ids := make([]int64, 0, len(pm.printers))
	for id := range pm.printers {
		ids = append(ids, id)
	}
	pm.mu.RUnlock()
	
	for _, id := range ids {
		_, _ = pm.CheckStatus(id)
	}
}

func (pm *PrinterManager) healthCheckLoop() {
	defer pm.wg.Done()
	
	interval := pm.config.HealthCheckInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	pm.CheckAllStatuses()
	
	for {
		select {
		case <-pm.stopCh:
			return
		case <-ticker.C:
			pm.CheckAllStatuses()
		}
	}
}

func (pm *PrinterManager) SendCommand(id int64, tspl string) error {
	pm.mu.RLock()
	p, exists := pm.printers[id]
	if !exists {
		pm.mu.RUnlock()
		return ErrPrinterNotFound
	}
	pm.mu.RUnlock()
	
	conn, err := pm.connect(id)
	if err != nil {
		return ErrPrinterOffline
	}
	
	timeout := pm.config.ConnectionTimeout
	if timeout == 0 {
		timeout = defaultReadWriteTimeout
	}
	
	_ = conn.SetDeadline(time.Now().Add(timeout))
	
	_, err = conn.Write([]byte(tspl))
	if err != nil {
		_ = conn.Close()
		pm.disconnect(id)
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	
	return nil
}

func (pm *PrinterManager) Print(id int64, tspl string, copies int) error {
	status, err := pm.CheckStatus(id)
	if err != nil {
		return err
	}
	
	if !status.IsOnline {
		return ErrPrinterOffline
	}
	
	if !status.CanPrint {
		return ErrPrinterCannotPrint
	}
	
	fullTSPL := tspl
	if copies > 1 {
		for i := 1; i < copies; i++ {
			fullTSPL += "\r\n" + tspl
		}
	}
	
	err = pm.SendCommand(id, fullTSPL)
	if err != nil {
		return err
	}
	
	pm.mu.Lock()
	if p, exists := pm.printers[id]; exists {
		p.TotalPrints += int64(copies)
	}
	pm.mu.Unlock()
	
	_, _ = pm.db.Exec(db.IncrementPrinterPrints, copies, id)
	
	return nil
}

func (pm *PrinterManager) PausePrinter(id int64) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	p, exists := pm.printers[id]
	if !exists {
		return ErrPrinterNotFound
	}
	
	oldStatus := p.Status
	p.Status = "paused"
	
	_, _ = pm.db.Exec(db.UpdatePrinterStatus, "paused", id)
	
	if oldStatus != "paused" && pm.webhookSender != nil {
		go pm.webhookSender.SendPrinterStatusChange(id, p.Name, oldStatus, "paused", nil)
	}
	
	return nil
}

func (pm *PrinterManager) ResumePrinter(id int64) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	p, exists := pm.printers[id]
	if !exists {
		return ErrPrinterNotFound
	}
	
	oldStatus := p.Status
	p.Status = "online"
	
	_, _ = pm.db.Exec(db.UpdatePrinterStatus, "online", id)
	
	if oldStatus != "online" && pm.webhookSender != nil {
		go pm.webhookSender.SendPrinterStatusChange(id, p.Name, oldStatus, "online", nil)
	}
	
	return nil
}

func (pm *PrinterManager) UpdatePrinter(p *Printer) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if _, exists := pm.printers[p.ID]; !exists {
		return ErrPrinterNotFound
	}
	
	_, err := pm.db.Exec(db.UpdatePrinter,
		p.Name, p.IPAddress, p.Port, p.DPI,
		p.LabelWidthMM, p.LabelHeightMM, p.GapMM, p.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update printer: %w", err)
	}
	
	pm.printers[p.ID] = p
	
	if conn, exists := pm.connections[p.ID]; exists && conn != nil {
		conn.Close()
		delete(pm.connections, p.ID)
	}
	
	return nil
}

func (pm *PrinterManager) GetConnection(id int64) (net.Conn, error) {
	return pm.connect(id)
}

func (pm *PrinterManager) CloseConnection(id int64) {
	pm.disconnect(id)
}
