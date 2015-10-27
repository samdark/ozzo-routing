// Copyright 2015 Qiang Xue. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package routing implements Sinatra-styled routing.
package routing

// todo: add ignoreTrailingSlash option
// todo: add default error handler

import (
	"regexp"
	"net/http"
	"strings"
	"reflect"
	"fmt"
)

// Handler is the type of the functions that can be associated with a router or route.
//
// A handler is a function whose parameter values are injected by Context according to the parameter types.
//
// Within a handler, call Context.Next() to pass the control to the next handler on the same route/router,
// or the first handler of the next matching route/router; call Context.NextRoute() to pass the control
// to the first handler of the next matching route/router; and call Context.Error() to trigger an error
// and pass the control to error handlers registered with the router.
type Handler interface{}

// Routable matches the specified HTTP method and URL path, and dispatches them to handlers for processing.
type Routable interface {
	// Match determines if the route matches the given HTTP method and URL path.
	// If so, it returns the URL path (possibly processed) and the URL parameter values
	Match(method, path string) (bool, string, map[string]string)
	// MatchPath determines if the route matches the given URL path.
	// MatchPath is similar to Match, except that it only matches the URL path.
	MatchPath(path string) (bool, string, map[string]string)
	// Dispatch processes the request by calling the handlers associated with this route.
	Dispatch(method, path string, c *Context)
}

// Router dispatches a request to its matching routes which will call their associated handlers.
//
// A router can be associated with a set of routes, each of which is associated with one or multiple handlers.
// If a route matches the current request's HTTP method and URL path, its handlers will be invoked.
//
// To register a route with a router, call To() or its shortcut methods, such as Get(), Post().
// When registering multiple routes sharing a common prefix in their URL paths, call Group() so that
// these routes can be more efficiently managed by a child router.
//
// Call Use() to register handlers (aka middlewares) that will be called for all requests.
// And call Error() to register error handlers that are only called when the router
// recovers a panic from a handler.
type Router struct {
	Parent   *Router         // the parent router
	Routes   []Routable      // routes and child routers associated with this router

	Methods  map[string]bool // the HTTP methods used to match the current HTTP method
	Pattern  string          // the pattern used to match request URL path
	Handlers []Handler       // handlers associated with the router

	regex    *regexp.Regexp  // the compiled regexp of the pattern
}

type DataWriter interface {
	WriteData(interface{}) (int, error)
}

// NewRouter creates an empty Router.
func NewRouter() *Router {
	return &Router{
		Methods: make(map[string]bool),
		Pattern: "",
	}
}

// NewChildRouter creates a new Router with the specified URL path prefix and handlers.
func NewChildRouter(pattern string, handlers []Handler) *Router {
	matches := routeRegex.FindStringSubmatch(pattern)
	if len(matches) != 3 {
		panic(RoutePatternError(pattern))
	}

	r := &Router{
		Methods: make(map[string]bool),
		Pattern: matches[2],
	}

	if len(matches[1]) > 0 {
		for _, method := range strings.Split(matches[1], ",") {
			r.Methods[method] = true
		}
	}

	validateHandlers(handlers)
	r.Handlers = append(r.Handlers, handlers...)

	if !literalRegex.MatchString(r.Pattern) {
		r.regex = regexp.MustCompile("^" + parseParamPattern(r.Pattern))
	}

	return r
}

// ServeHTTP dispatches the request to the handlers of the matching route(s).
// ServeHTTP is the method required by http.Handler
func (r *Router) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	r.Dispatch(req.Method, req.URL.Path, NewContext(res, req))
}

// Group adds a set of routes that are grouped together by a common URL path prefix.
// The routes to be added should be specified in func(*Router). For example,
//
//   router := routing.New()
//   router.Group("/admin", func(r *routing.Router) {
//     r.Get("/users", func() { ... })
//     r.Post("/users", func() { ... })
//   })
//
func (r *Router) Group(pattern string, rt func(*Router), handlers ...Handler) {
	router := NewChildRouter(pattern, handlers)
	router.Parent = r
	r.Routes = append(r.Routes, router)
	rt(router)
}

// To creates a new route using the specified URL path pattern and adds it to the router.
//
// The pattern should be in the format of "GET,POST /users/<id:\\d+>", where "GET,POST" represents
// which HTTP methods the route should match, and "</users/<id:\\d+>" is a regular expression used
// to match the request URL path. If no HTTP method is specified in the pattern, it means the route
// matches any HTTP method.
//
// The pattern can contain tokens in the format of "<ParamName:Pattern>", which allows the route
// to capture specific parts of a URL path and make them accessible via Context.Params[ParamName].
// The "Pattern" in the token is a regular expression that the parameter should match.
// If "Pattern" is not provided, it will default to "[^/]+", meaning the parameter should match
// a string without any forward slash.
//
// For example,
//
//   router := routing.New()
//   router.To("GET,POST /users", func() { })
//   router.To("/posts", func() { })
//
// A route can be associated with multiple handlers (functions) which will be called one after another
// when the route matches the current request. A handler can have parameters whose values will be injected
// by Context according to their types. Please refer to Context for more details about dependency injection.
//
// Within a handler, you can call Context.Next() to pass the control to the next handler on the same route
// or the first handler of the next matching route. You can also call Context.NextRoute() to pass the control
// to the first handler of the next matching route. For example,
//
//   router := routing.New()
//   router.To("/auth", func (c *routing.Context) {
//       // ...do authentication here
//       c.Next()
//       // ...cleanup work here
//   })
func (r *Router) To(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute(pattern, handlers))
}

// Use is a shortcut for To(). It adds handlers to a route that matches any request.
// Use is mainly used to register handlers that are known as middlewares.
func (r *Router) Use(handlers ...Handler) *Route {
	return r.AddRoute(NewRoute(".*", handlers))
}

// Error adds error handlers to the router.
// An error handler will be invoked when a panic caused by a prior handler is recovered
// and recorded as Context.Error. An error handler is like a regular handler in which
// you can call Context.Next() to pass the control to the next error handler.
func (r *Router) Error(handlers ...Handler) *Route {
	route := NewRoute(".*", handlers)
	route.err = true
	return r.AddRoute(route)
}

// Get is a shortcut for To(). It adds handlers to a route that only matches GET HTTP method.
func (r *Router) Get(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("GET " + pattern, handlers))
}

// Post is a shortcut for To(). It adds handlers to a route that only matches POST HTTP method.
func (r *Router) Post(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("POST " + pattern, handlers))
}

// Put is a shortcut for To(). It adds handlers to a route that only matches PUT HTTP method.
func (r *Router) Put(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("PUT " + pattern, handlers))
}

// Patch is a shortcut for To(). It adds handlers to a route that only matches PATCH HTTP method.
func (r *Router) Patch(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("PATCH " + pattern, handlers))
}

// Delete is a shortcut for To(). It adds handlers to a route that only matches DELETE HTTP method.
func (r *Router) Delete(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("DELETE " + pattern, handlers))
}

// Head is a shortcut for To(). It adds handlers to a route that only matches HEAD HTTP method.
func (r *Router) Head(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("HEAD " + pattern, handlers))
}

// Options is a shortcut for To(). It adds handlers to a route that only matches OPTIONS HTTP method.
func (r *Router) Options(pattern string, handlers ...Handler) *Route {
	return r.AddRoute(NewRoute("OPTIONS " + pattern, handlers))
}

// AddRoute adds a route to the router. The same route object is returned to allow further method chaining.
func (r *Router) AddRoute(route *Route) *Route {
	r.Routes = append(r.Routes, route)
	return route
}

// Match checks if the router matches the specified HTTP method and URL path.
func (r *Router) Match(method, path string) (bool, string, map[string]string) {
	if len(r.Methods) > 0 && !r.Methods[method] {
		return false, path, nil
	}
	return r.MatchPath(path)
}

// MatchPath determines if the route matches the given URL path.
// MatchPath is similar to Match, except that it only matches the URL path.
func (r *Router) MatchPath(path string) (bool, string, map[string]string) {
	if r.regex == nil {
		if strings.HasPrefix(path, r.Pattern) {
			return true, path[len(r.Pattern):], make(map[string]string)
		}
		return false, path, nil
	}

	matches := r.regex.FindStringSubmatch(path)
	if len(matches) == 0 {
		return false, path, nil
	}

	params := make(map[string]string)
	for i, name := range r.regex.SubexpNames() {
		if len(name) > 0 {
			params[name] = matches[i]
		}
	}

	return true, path[len(matches[0]):], params
}


// Dispatch invokes the handlers of the routes that match the specified HTTP method and URL path.
func (r *Router) Dispatch(method, path string, context *Context) {
	handlerIndex := 0
	routeIndex := 0
	oldNext := context.Next
	oldNextRoute := context.NextRoute
	oldParams := context.Params

	// using closures to keep states for recursions: all above local vars are recursion states

	nextFunc := func() {
		// calling handlers directly associated with the router
		if handlerIndex < len(r.Handlers) && context.Error == nil {
			handler := r.Handlers[handlerIndex]
			handlerIndex++
			// should use the parent router's nextFunc()  as Context.NextRoute()
			// so that when NextRoute() is called it will jump to the next route
			// of the parent router
			newNextRoute := context.NextRoute
			context.NextRoute = oldNextRoute
			callHandler(context, handler)
			context.NextRoute = newNextRoute
			return
		}

		// calling handlers associated with the routes directly under this router
		for routeIndex < len(r.Routes) {
			route := r.Routes[routeIndex]
			routeIndex++
			if matching, p, params := route.Match(method, path); matching {
				if len(params) > 0 {
					context.Params = copyParams(oldParams)
					for name, value := range params {
						context.Params[name] = value
					}
				}
				route.Dispatch(method, p, context)
				return
			}
		}

		// prepare to call parent router's handlers
		context.Next = oldNext
		context.NextRoute = oldNextRoute
		context.Params = oldParams

		// call parent router's nextFunc
		oldNextRoute()
	}

	context.Next = nextFunc
	context.NextRoute = nextFunc

	nextFunc()
}

func copyParams(params map[string]string) map[string]string {
	r := make(map[string]string)
	for k, v := range params {
		r[k] = v
	}
	return r
}

func validateHandlers(handlers []Handler) {
	for _, handler := range handlers {
		t := reflect.TypeOf(handler)
		if t.Kind() != reflect.Func {
			panic("a handler must be a callable function")
		}
		if t.NumOut() > 1 {
			panic("a handler can return at most one value")
		}
	}
}

func callHandler(c *Context, fn Handler) {
	defer func() {
		if err := recover(); err != nil {
			c.Error = err
			c.NextRoute()
		}
	}()

	result := c.Call(fn)
	if len(result) == 0 {
		return
	}
	output := result[0]

	// use DataWriter to write response if possible
	if dw, ok := c.Response.(DataWriter); ok {
		if _, err := dw.WriteData(output); err != nil {
			panic(err)
		}
		return
	}

	switch output.(type) {
	case []byte:
		c.Response.Write(output.([]byte))
	case string:
		c.Response.Write([]byte(output.(string)))
	default:
		if output != nil {
			fmt.Fprint(c.Response, output)
		}
	}
}