-- Registration must run only after the organization head was actually inserted.
-- BEFORE INSERT also fires for ON CONFLICT no-ops and can create orphan records
-- or reject a previously valid idempotent organization configuration save.
DROP TRIGGER model_heads_register ON model_configuration_heads;
CREATE TRIGGER model_heads_register AFTER INSERT ON model_configuration_heads
FOR EACH ROW EXECUTE FUNCTION public.hc_register_organization_model();
