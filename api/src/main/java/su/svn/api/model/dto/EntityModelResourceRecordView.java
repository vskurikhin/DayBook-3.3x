/*
 * This file was last modified at 2026.05.07 14:57 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * EntityModelResourceRecordView.java
 * $Id$
 */

package su.svn.api.model.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.UUID;

/**
 * EntityModelResourceRecordView
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record EntityModelResourceRecordView(
        @JsonProperty UUID id,
        @JsonProperty Boolean visible,
        @JsonProperty Integer flags,
        @JsonProperty UUID parentId,
        @JsonProperty String userName,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty LocalDateTime lastChangedTime,
        @JsonProperty String title,
        @JsonProperty Map<String, String> values,
        @JsonProperty("_links") Map<String, Link> links) implements Serializable {

    @SuppressWarnings("ReassignedVariable")
    @Builder
    public EntityModelResourceRecordView {
        if (values == null) values = new LinkedHashMap<>();
        if (links == null) links = new LinkedHashMap<>();
    }

    public EntityModelResourceRecordView() {
        this(UUID.randomUUID(), false, 0,
                new UUID(0, 0),
                null,
                OffsetDateTime.now(), null, LocalDateTime.now(),
                null,
                new HashMap<>(), new HashMap<>()
        );
    }
}
