import { useCallback, useEffect } from "react";
import {
  createBrowserRouter,
  RouterProvider,
  useLocation,
  Navigate,
} from "react-router-dom";
import PropTypes from "prop-types";
import axios from "axios";

import Signup from "./components/Signup/Signup";
import Login from "./components/Login/Login";
import Home from "./components/Home/Home";
import Users from "./components/Users/Users";
import Layout from "./components/Layout/Layout";
import SplashScreen from "./components/SplashScreen/SplashScreen";

import { useAuth } from "./contexts/auth-context";
import { STATUS } from "./utils/utils";

function App() {
  const { login, logout, isAuthenticated, expires_at } = useAuth();
  console.log("isAuthenticated: " + isAuthenticated + " expires_at: " + expires_at)

  const refreshAccessToken = useCallback(async () => {
    try {
      const response = await axios.post(
        "/auth/api/v2/refresh",
        {},
        {
          withCredentials: true,
        }
      );

      const { user, token, expires_at } = response.data.data;

      if (response.status === 204 || response.status === 401) {
        console.log("logout: " + logout)
        logout();
      } else {
        console.log("login() user: " + user + " token: " + token + " expires_at: " + expires_at)
        login(user, token, expires_at);
      }
    } catch (error) {
      logout();
    }
  }, [login, logout]);

  useEffect(() => {
    refreshAccessToken();
  }, [refreshAccessToken]);

  useEffect(() => {
    let refreshAccessTokenTimerId;

    if (isAuthenticated) {
      refreshAccessTokenTimerId = setTimeout(() => {
        refreshAccessToken();
      }, new Date(expires_at).getTime() - Date.now() - 10 * 1000);
    }

    return () => {
      if (isAuthenticated && refreshAccessTokenTimerId) {
        clearTimeout(refreshAccessTokenTimerId);
      }
    };
  }, [expires_at, isAuthenticated, refreshAccessToken]);

// New code
  const router = createBrowserRouter([
    {
      element: <Layout />,
      children: [
        {
          path: "/",
          element: (
            <RequireAuth redirectTo="/sign-up">
              <Home />
            </RequireAuth>
          ),
        },
        {
          path: "sign-up",
          element: (
            <RedirectIfLoggedIn redirectTo="/">
              <Signup />
            </RedirectIfLoggedIn>
          ),
        },
        {
          path: "login",
          element: (
            <RedirectIfLoggedIn redirectTo="/">
              <Login />
            </RedirectIfLoggedIn>
          ),
        },
        {
          path: "users",
          element: (
            <RequireAuth redirectTo="/sign-up">
              <Users />
            </RequireAuth>
          ),
        }
      ],
    },
  ]);

  return (
    <div className="App">
      <RouterProvider router={router} />
    </div>
  );
}

// New code
const RequireAuth = ({ children, redirectTo }) => {
  const { isAuthenticated, status } = useAuth();
  const location = useLocation();
  console.log("status: " + status)

  if (status === STATUS.PENDING) return <SplashScreen />;

  return isAuthenticated ? (
    children
  ) : (
    <Navigate to={redirectTo} state={{ from: location }} />
  );
};

// New code
const RedirectIfLoggedIn = ({ children, redirectTo }) => {
  const { isAuthenticated, status } = useAuth();
  const location = useLocation();

  if (status === STATUS.PENDING) return <SplashScreen />;

  return isAuthenticated ? (
    <Navigate to={location.state?.from?.pathname || redirectTo} />
  ) : (
    children
  );
};

RequireAuth.propTypes = {
  children: PropTypes.element.isRequired,
  redirectTo: PropTypes.string.isRequired,
};

RedirectIfLoggedIn.propTypes = {
  children: PropTypes.element.isRequired,
  redirectTo: PropTypes.string.isRequired,
};

export default App;