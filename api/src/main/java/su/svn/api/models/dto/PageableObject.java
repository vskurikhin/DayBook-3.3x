/*
 * This file was last modified at 2026.05.21 16:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PageableObject.java
 * $Id$
 */

package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;

@Deprecated
@JsonInclude(JsonInclude.Include.NON_NULL)
public record PageableObject(
        @JsonProperty Long offset,
        @JsonProperty SortObject sort,
        @JsonProperty Boolean paged,
        @JsonProperty Integer pageNumber,
        @JsonProperty Integer pageSize,
        @JsonProperty Boolean unpaged) implements Serializable {
    @Builder
    public PageableObject {
    }
}
