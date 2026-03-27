/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ResourceBaseRecord.java
 * $Id$
 */

package su.svn.core.models.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.annotation.JsonPropertyOrder;
import lombok.Builder;

import java.io.Serializable;
import java.time.OffsetDateTime;
import java.util.UUID;

@JsonPropertyOrder({"id", "visible", "flags"})
@JsonInclude(JsonInclude.Include.NON_NULL)
public record ResourceBaseRecord(
        @JsonProperty UUID id,
        @JsonProperty UUID parentId,
        @JsonIgnore String userName,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty boolean visible,
        @JsonProperty int flags) implements Serializable {
    @Builder(toBuilder = true)
    public ResourceBaseRecord {
    }
}
