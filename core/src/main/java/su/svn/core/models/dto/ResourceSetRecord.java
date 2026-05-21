/*
 * This file was last modified at 2026.05.21 23:42 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ResourceSetRecord.java
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
import java.util.HashSet;
import java.util.Set;
import java.util.UUID;

/**
 * DTO representing a set record returned to clients.
 *
 * @param id record identifier
 * @param parentId parent record identifier
 * @param title record title
 * @param texts set of unique string values
 * @param userName owner username
 * @param postAt creation timestamp
 * @param refreshAt update timestamp
 * @param visible visibility flag
 * @param flags custom bit flags
 * @param tags associated tags
 */
@JsonPropertyOrder({"id", "visible", "flags"})
@JsonInclude(JsonInclude.Include.NON_NULL)
public record ResourceSetRecord(
        @JsonProperty UUID id,
        @JsonProperty UUID parentId,
        @JsonProperty String title,
        @JsonProperty Set<String> texts,
        @JsonIgnore String userName,
        @JsonProperty OffsetDateTime postAt,
        @JsonProperty OffsetDateTime refreshAt,
        @JsonProperty boolean visible,
        @JsonProperty int flags,
        @JsonProperty Set<String> tags) implements Serializable {
    @SuppressWarnings("ReassignedVariable")
    @Builder(toBuilder = true)
    public ResourceSetRecord {
        // if (texts == null) texts = new HashSet<>();
        if (tags == null) tags = new HashSet<>();
    }
}
