/*
 * This file was last modified at 2026.04.04 13:04 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * Page.java
 * $Id$
 */

package su.svn.api.model.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record Page<T>(
        @JsonProperty List<T> list,
        @JsonProperty int pageCount,
        @JsonProperty int pageIndex,
        @JsonProperty int pageSize,
        @JsonProperty boolean hasNextPage,
        @JsonProperty boolean hasPreviousPage) implements Serializable {
    @Builder
    public Page {
    }
}
