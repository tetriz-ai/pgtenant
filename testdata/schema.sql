--
-- PostgreSQL database dump
--

\restrict z0AUWhyRaWNLcocxAoEZM0PSMspmfAcEjFOLHDzglOvmvLdmnrOFKwXiuAKjG3W

-- Dumped from database version 17.11 (Debian 17.11-1.pgdg13+2)
-- Dumped by pg_dump version 17.9 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

--
-- Name: note_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.note_events (
    note_id uuid NOT NULL,
    happened_on date NOT NULL,
    kind text NOT NULL
)
PARTITION BY RANGE (happened_on);


SET default_table_access_method = heap;

--
-- Name: note_events_2026; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.note_events_2026 (
    note_id uuid NOT NULL,
    happened_on date NOT NULL,
    kind text NOT NULL
);


--
-- Name: notes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    body text NOT NULL,
    pinned boolean DEFAULT false NOT NULL,
    created_on date DEFAULT CURRENT_DATE NOT NULL
);


--
-- Name: organizations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.organizations (
    id uuid NOT NULL,
    name text NOT NULL
);


--
-- Name: note_events_2026; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.note_events ATTACH PARTITION public.note_events_2026 FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');


--
-- Name: notes notes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notes
    ADD CONSTRAINT notes_pkey PRIMARY KEY (id);


--
-- Name: organizations organizations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.organizations
    ADD CONSTRAINT organizations_pkey PRIMARY KEY (id);


--
-- Name: notes_one_pinned_per_org; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX notes_one_pinned_per_org ON public.notes USING btree (organization_id) WHERE pinned;


--
-- Name: notes notes_organization_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notes
    ADD CONSTRAINT notes_organization_id_fkey FOREIGN KEY (organization_id) REFERENCES public.organizations(id);


--
-- Name: notes cutoff_window; Type: POLICY; Schema: public; Owner: -
--

CREATE POLICY cutoff_window ON public.notes AS RESTRICTIVE FOR SELECT USING ((created_on >= (COALESCE(NULLIF(current_setting('app.data_cutoff'::text, true), ''::text), '-infinity'::text))::date));


--
-- Name: notes; Type: ROW SECURITY; Schema: public; Owner: -
--

ALTER TABLE public.notes ENABLE ROW LEVEL SECURITY;

--
-- Name: notes tenant_isolation; Type: POLICY; Schema: public; Owner: -
--

CREATE POLICY tenant_isolation ON public.notes USING (((current_setting('app.current_tenant'::text, true) = '__system__'::text) OR ((organization_id)::text = current_setting('app.current_tenant'::text, true)))) WITH CHECK (((current_setting('app.current_tenant'::text, true) = '__system__'::text) OR ((organization_id)::text = current_setting('app.current_tenant'::text, true))));


--
-- PostgreSQL database dump complete
--

\unrestrict z0AUWhyRaWNLcocxAoEZM0PSMspmfAcEjFOLHDzglOvmvLdmnrOFKwXiuAKjG3W

