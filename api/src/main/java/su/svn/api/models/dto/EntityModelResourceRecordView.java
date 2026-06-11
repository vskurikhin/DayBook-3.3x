/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * EntityModelResourceRecordView.java
 * $Id$
 */

package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;
import su.svn.lib.RecordType;

import java.io.Serializable;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.*;

/**
 * EntityModelResourceRecordView
 */
@JsonInclude(JsonInclude.Include.NON_ABSENT)
public record EntityModelResourceRecordView(
        @JsonProperty UUID id,
        @JsonProperty Boolean visible,
        @JsonProperty Integer flags,
        @JsonProperty UUID parentId,
        @JsonProperty su.svn.lib.RecordType type,
        @JsonProperty String userName,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty LocalDateTime lastChangedTime,
        @JsonProperty String title,
        @JsonProperty String aHref,
        @JsonProperty byte[] blob,
        @JsonProperty Map<String, String> json,
        @JsonProperty Set<String> texts,
        @JsonProperty String fileName,
        @JsonProperty String html,
        @JsonProperty String link,
        @JsonProperty String markdown,
        @JsonProperty String value,
        @JsonProperty float[] vector,
        @JsonProperty String xml,
        @JsonProperty List<String> tags,
        @JsonProperty("_links") Map<String, Link> links) implements Serializable {

    @Builder
    public EntityModelResourceRecordView {
        if (links == null) links = new LinkedHashMap<>();
        if (type == null) type = RecordType.Base;
    }

    public EntityModelResourceRecordView() {
        this(UUID.randomUUID(), false, 0, new UUID(0, 0), RecordType.Base,
                null,
                OffsetDateTime.now(), null, LocalDateTime.now(),
                null, null, null, null, null, null, null, null, null, null, null, null, null,
                new HashMap<>()
        );
    }
}
