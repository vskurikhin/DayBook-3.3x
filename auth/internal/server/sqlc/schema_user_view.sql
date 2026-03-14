--
-- Name: user_view; Type: TABLE; Schema: public; Owner: dbuser
--

CREATE TABLE user_view (
    user_name character varying(64),
    id uuid,
    password character varying(1024),
    create_time timestamp without time zone,
    update_time timestamp without time zone,
    enabled boolean,
    local_change boolean,
    visible boolean,
    flags integer,
    roles text[]
);
