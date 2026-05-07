package su.svn.lib;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class ConstantsTest {

    @Test
    void shouldReturnCorrectValue() {
        assertEquals("REQUEST_ID", Constants.All.getValue());
    }

    @Test
    void shouldReturnNullValueForNullEnum() {
        assertNull(Constants.Null.getValue());
    }

    @Test
    void shouldFindConstantByKey() {
        Constants result = Constants.lookup("REQUEST_ID");

        assertEquals(Constants.All, result);
    }

    @Test
    void shouldReturnNullConstantWhenLookupKeyIsNull() {
        Constants result = Constants.lookup(null);

        assertEquals(Constants.Null, result);
    }

    @Test
    void shouldReturnNullWhenKeyNotFound() {
        Constants result = Constants.lookup("UNKNOWN");

        assertNull(result);
    }

    @Test
    void shouldReturnTrueWhenStringEqualsMatches() {
        boolean result = Constants.All.stringEquals("REQUEST_ID");

        assertTrue(result);
    }

    @Test
    void shouldReturnFalseWhenStringDoesNotMatch() {
        boolean result = Constants.All.stringEquals("OTHER");

        assertFalse(result);
    }

    @Test
    void shouldReturnTrueWhenOtherIsNull() {
        boolean result = Constants.All.stringEquals(null);

        assertTrue(result);
    }

    @Test
    void shouldReturnTrueWhenBothValuesAreNull() {
        boolean result = Constants.Null.stringEquals(null);

        assertTrue(result);
    }

    @Test
    void shouldReturnFalseWhenEnumValueIsNullAndOtherNotNull() {
        boolean result = Constants.Null.stringEquals("REQUEST_ID");

        assertFalse(result);
    }
}