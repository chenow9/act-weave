DROP TRIGGER IF EXISTS audit_exports_object_guard ON audit_exports;
DROP FUNCTION IF EXISTS enforce_audit_export_object();
DROP TABLE IF EXISTS audit_exports;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS audit_events CASCADE;
DROP FUNCTION IF EXISTS reject_audit_event_mutation();
